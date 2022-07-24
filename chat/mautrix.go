package chat

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	retry "github.com/sethvargo/go-retry"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	mcrypto "maunium.net/go/mautrix/crypto"
	mevent "maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	mid "maunium.net/go/mautrix/id"
)

type BotPlexer struct {
	recipient   *string
	username    *string
	matrix_srvr *string
	password    *string // only kept until connect
	client      *mautrix.Client
	olmMachine  *mcrypto.OlmMachine
	encrypted   bool
	stateStore  *StateStore
	timeout     int
	Ch          chan *mevent.Event
	db          Database
}

type CryptoLogger struct{}

var _ crypto.Logger = &CryptoLogger{}

func (f CryptoLogger) Error(message string, args ...interface{}) {
	log.Errorf(message, args...)
}

func (f CryptoLogger) Warn(message string, args ...interface{}) {
	log.Warnf(message, args...)
}

func (f CryptoLogger) Debug(message string, args ...interface{}) {
	log.Debugf(message, args...)
}

func (f CryptoLogger) Trace(message string, args ...interface{}) {
	log.Tracef(message, args...)
}

func NewApp(timeout string, encrypted bool, db Database) *BotPlexer {
	res, _ := strconv.Atoi(timeout)
	return &BotPlexer{
		new(string),
		new(string),
		new(string),
		new(string),
		nil,
		nil,
		encrypted,
		nil,
		res,
		make(chan *mevent.Event, 8),
		db,
	}
}

type Matrix struct {
	UserID      *string `db:"userid"`
	Created     *string `db:"created"`
	AccessToken *string `db:"token"`
}

func (m *Matrix) GetByPk(pk string) error {
	re := regexp.MustCompile("[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}")
	var inderface interface{}
	inderface = pk
	DB.GetByPk(m, inderface, "userid")
	*m.Created = re.FindStringSubmatch(*m.Created)[0]
	return nil
}

func NewMatrixSession(userid, token, created string) *Matrix {
	_UserID := new(string)
	_AccessToken := new(string)
	_Created := new(string)
	_UserID = &userid
	_AccessToken = &token
	_Created = &created
	return &Matrix{
		UserID:      _UserID,
		AccessToken: _AccessToken,
		Created:     _Created,
	}
}

func (m *Matrix) Create() error {
	err := DB.InsertRow(m)
	if err != nil {
		return err
	}
	return nil
}

func (m *Matrix) Save() error {
	err := DB.UpdateRowPk(m, "userid")
	if err != nil {
		return err
	}
	return nil
}

func UseSession(client *mautrix.Client, token, username string) error {
	client.AccessToken = token
	client.UserID = (id.UserID)(username)
	_, err := client.GetOwnPresence()
	return err
}

func CreateSession(client *mautrix.Client, password, username string, session *Matrix) error {
	reqlogin := &mautrix.ReqLogin{
		Type: mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{
			Type: mautrix.IdentifierTypeUser,
			User: username,
		},
		Password:                 password,
		InitialDeviceDisplayName: "livematrix",
		DeviceID:                 "livematrix",
		StoreCredentials:         true,
	}
	_, err := DoRetry("login:pass", func() (interface{}, error) {
		return client.Login(reqlogin)
	})

	if err == nil {
		format := "2006-01-02 15:04:05"
		created := time.Now().Format(format)
		if session == nil {
			session = NewMatrixSession(client.UserID.String(), client.AccessToken, created)
			session.Create()
		} else {
			session.AccessToken = &client.AccessToken
			session.Created = &created
			session.Save()
		}
	}
	return err
}

func (b *BotPlexer) Sync(encrypted bool) (*mautrix.DefaultSyncer, error) {
	if encrypted {
		return b.CryptoSync()
	}

	syncer := b.client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(mevent.EventMessage, func(source mautrix.EventSource, event *mevent.Event) { go b.HandleMessage(source, event) })

	return syncer, nil
}

func (b *BotPlexer) CryptoSync() (*mautrix.DefaultSyncer, error) {
	syncer := b.client.Syncer.(*mautrix.DefaultSyncer)

	// Setup the crypto store
	sqlCryptoStore := mcrypto.NewSQLCryptoStore(
		b.db.GetDB(),
		"sqlite3",
		*b.username,
		b.client.DeviceID,
		[]byte("standupbot_cryptostore_key"),
		CryptoLogger{},
	)
	err := sqlCryptoStore.CreateTables()
	if err != nil {
		log.Fatal("Could not create tables for the SQL crypto store:%v", err.Error())
	}

	b.olmMachine = mcrypto.NewOlmMachine(b.client, &CryptoLogger{}, sqlCryptoStore, b.stateStore)
	err = b.olmMachine.Load()

	if err != nil {
		log.Errorf("Could not initialize encryption support. Encrypted rooms will not work.")
		return nil, err
	}

	syncer.OnSync(func(resp *mautrix.RespSync, since string) bool {
		b.olmMachine.ProcessSyncResponse(resp, since)
		return true
	})

	syncer.OnEventType(mevent.StateMember, func(_ mautrix.EventSource, event *mevent.Event) {
		b.olmMachine.HandleMemberEvent(event)
		b.stateStore.SetMembership(event)

		if event.GetStateKey() == *b.username && event.Content.AsMember().Membership == mevent.MembershipInvite {
			log.Info("Joining ", event.RoomID)
			_, err := DoRetry("join room", func() (interface{}, error) {
				return b.client.JoinRoomByID(event.RoomID)
			})
			if err != nil {
				log.Errorf("Could not join channel %s. Error %+v", event.RoomID.String(), err)
			} else {
				log.Infof("Joined %s sucessfully", event.RoomID.String())
			}
		} else if event.GetStateKey() == *b.username && event.Content.AsMember().Membership.IsLeaveOrBan() {
			log.Infof("Left or banned from %s", event.RoomID)
		}
	})

	syncer.OnEventType(mevent.StateEncryption, func(_ mautrix.EventSource, event *mevent.Event) {
		b.stateStore.SetEncryptionEvent(event)
	})

	syncer.OnEventType(mevent.EventMessage, func(source mautrix.EventSource, event *mevent.Event) { go b.HandleMessage(source, event) })

	syncer.OnEventType(mevent.EventEncrypted, func(source mautrix.EventSource, event *mevent.Event) {
		decryptedEvent, err := b.olmMachine.DecryptMegolmEvent(event)
		if err != nil {
			log.Errorf("Failed to decrypt message from %s in %s: %+v", event.Sender, event.RoomID, err)
		} else {
			log.Debugf("Received encrypted event from %s in %s", event.Sender, event.RoomID)
			if decryptedEvent.Type == mevent.EventMessage {
				go b.HandleMessage(source, decryptedEvent)
			}
		}
	})
	return syncer, nil
}

func (b *BotPlexer) Connect(recipient, srvr, uname, passwd string, encrypted bool) {
	var err error
	b.stateStore = NewStateStore(b.db.GetDB())
	if err := b.stateStore.CreateTables(); err != nil {
		log.Fatal("Failed to create the tables for livematrix", err)
	}
	username := mid.UserID(uname).String()
	b.stateStore = NewStateStore(b.db.GetDB())
	*b.recipient = recipient
	*b.username = uname
	*b.password = passwd

	log.Infof("Logging in %s", username)
	b.client, err = mautrix.NewClient(srvr, "", "")
	if err != nil {
		panic(err)
	}

	session := NewMatrixSession("", "", "")
	session.GetByPk(username)

	created, _ := time.Parse("2006-1-2 15:4:5", *session.Created)

	if *session.AccessToken != "" && created.Add(time.Duration(b.timeout)*24*time.Hour).After(time.Now()) {
		err = UseSession(b.client, *session.AccessToken, username)
	} else {
		err = CreateSession(b.client, *b.password, username, nil)
	}

	if err != nil && *session.AccessToken != "" {
		log.Warningf("Could not login using access token: %s", err.Error())
		err = CreateSession(b.client, *b.password, username, session)
	}

	if err != nil {
		log.Fatalf("Couldn't login to the homeserver.")
	}

	syncer, err := b.Sync(encrypted)
	if err != nil || syncer == nil {
		log.Errorf("Error occurred: %v", err.Error())
	}

	log.Infof("Logged in as %s/%s", b.client.UserID, b.client.DeviceID)

	for {
		log.Debugf("Running sync...")
		err = b.client.Sync()
		if err != nil {
			log.Errorf("Sync failed. %+v", err)
		}
	}
}

func (b *BotPlexer) GetMessages(roomid mid.RoomID, offset int) []*JSONMessage {
	//TODO
	return nil
}

// There's no goroutine running this function... you have to spawn it somewhere
func (b *BotPlexer) CreateRoom(client *Client, encrypted bool) (resp mid.RoomID, err error) {
	var preset string
	if encrypted {
		preset = "private_chat"
	} else {
		preset = "public_chat"
	}
	response, err := b.client.CreateRoom(&mautrix.ReqCreateRoom{
		Preset:        preset,
		RoomAliasName: (*client.session.Alias) + "_" + (*client.session.SessionId)[:6],
		Topic:         "livechat",
		Invite:        []id.UserID{id.UserID(*b.recipient)},
	})

	if err != nil {
		return "", err
	}

	return response.RoomID, nil
}

func (b *BotPlexer) JoinRoomByID(rid mid.RoomID) (*mautrix.RespJoinRoom, error) {
	return b.client.JoinRoomByID(rid)
}

func DoRetry(description string, fn func() (interface{}, error)) (interface{}, error) {
	var err error
	b := retry.NewFibonacci(1 * time.Second)
	b = retry.WithMaxRetries(3, b)
	for {
		log.Info("trying: ", description)
		var val interface{}
		val, err = fn()
		if err == nil {
			// Success
			return val, nil
		}
		nextDuration, stop := b.Next()
		// Retrying...
		if stop {
			err = errors.New("%s failed. Retry limit reached. Will not retry.")
			break
		}
		time.Sleep(nextDuration)
	}
	return nil, err
}

func (b *BotPlexer) HandleMessage(source mautrix.EventSource, event *mevent.Event) {
	// If event is from ourselves, ignore
	if event.Sender.String() == *b.username {
		return
	} else {
		b.Ch <- event
	}
}

func (b *BotPlexer) SendMessage(roomId mid.RoomID, content *mevent.MessageEventContent) (resp *mautrix.RespSendEvent, err error) {
	eventContent := &mevent.Content{Parsed: content}
	r, err := DoRetry(fmt.Sprintf("send message to %s", roomId), func() (interface{}, error) {
		//Sending unencrypted event
		return b.client.SendMessageEvent(roomId, mevent.EventMessage, eventContent)
	})
	if err != nil {
		log.Errorf("Failed to send message to %s: %s", roomId, err)
		return nil, err
	}
	return r.(*mautrix.RespSendEvent), err
}
