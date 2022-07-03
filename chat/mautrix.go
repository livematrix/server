package chat

import (
	"errors"
	"fmt"
	"time"

	retry "github.com/sethvargo/go-retry"
	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
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
	timewait    float64
	Ch          chan *mevent.Event
}

type Matrix struct {
	UserID      *string `db:"userid"`
	Created     *string `db:"created"`
	AccessToken *string `db:"token"`
}

func (m *Matrix) GetByPk(pk string) error {
	var inderface interface{}
	inderface = pk
	DB.GetByPk(m, inderface, "userid")
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

var App BotPlexer
var username string

func NewApp() *BotPlexer {
	return &BotPlexer{
		new(string),
		new(string),
		new(string),
		new(string),
		nil,
		1,
		make(chan *mevent.Event, 8),
	}
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
		if session.AccessToken == nil {
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

func (b *BotPlexer) Connect(recipient, srvr, uname, passwd string) {
	var err error
	b.timewait = 30
	username = mid.UserID(uname).String()
	*b.recipient = recipient
	*b.username = uname
	*b.password = passwd

	log.Infof("Logging in %s", username)

	b.client, err = mautrix.NewClient(srvr, "", "")
	if err != nil {
		panic(err)
	}

	newSession := NewMatrixSession("", "", "")
	newSession.GetByPk(username)

	if newSession.AccessToken != nil {
		err = UseSession(b.client, *newSession.AccessToken, username)
	} else {
		err = CreateSession(b.client, *b.password, username, nil)
	}

	if err != nil && newSession.AccessToken != nil {
		log.Warning("Could not login using access token: %v", err.Error())
		err = CreateSession(b.client, *b.password, username, newSession)
	}

	if err != nil {
		log.Fatalf("Couldn't login to the homeserver.")
	}

	syncer := b.client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(mevent.EventMessage, func(source mautrix.EventSource, event *mevent.Event) { go b.HandleMessage(source, event) })

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
func (b *BotPlexer) CreateRoom(client *Client) (resp mid.RoomID, err error) {
	response, err := b.client.CreateRoom(&mautrix.ReqCreateRoom{
		Preset:        "public_chat",
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
