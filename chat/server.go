package chat

import (
	"fmt"
	"log"
	"net/http"

	"golang.org/x/net/websocket"
	"maunium.net/go/mautrix/format"
	mid "maunium.net/go/mautrix/id"
)

// One catalog for each client, to store all websockets and chat history
type ClientIndex struct {
	clients map[int]*Client //Each of these are independent sockets for the same client
	history []*Message      //Preserve messages from same client, as sockets are removed
}

func NewClientIndex() *ClientIndex {
	clients := make(map[int]*Client)
	history := []*Message{}

	return &ClientIndex{
		clients,
		history,
	}
}

// Chat server.
type Server struct {
	encrypted      bool
	pattern        string
	messages       []*JSONMessage
	clients        map[string]*ClientIndex
	addCh          chan *Client
	delCh          chan *Client
	sendAllCh      chan *JSONMessage
	doneCh         chan bool
	errCh          chan error
	Mautrix_client *BotPlexer
}

// Create new chat server.
func NewServer(pattern string, encrypted bool, mautrix_client *BotPlexer) *Server {
	messages := []*JSONMessage{}
	clients := make(map[string]*ClientIndex)
	addCh := make(chan *Client)
	delCh := make(chan *Client)
	sendAllCh := make(chan *JSONMessage)
	doneCh := make(chan bool)
	errCh := make(chan error)

	return &Server{
		encrypted,
		pattern,
		messages,
		clients,
		addCh,
		delCh,
		sendAllCh,
		doneCh,
		errCh,
		mautrix_client,
	}
}

func (s *Server) FindClientByRoomID(roomid mid.RoomID) (Client, error) {

	for _, client := range s.clients {
		for _, client_id := range client.clients {
			if c := client_id.GetRoomId(); mid.RoomID(*c) == roomid {
				return *client_id, nil
			}
		}
	}
	return Client{}, error(fmt.Errorf("No clients with such RoomID"))
}

func (s *Server) Add(c *Client) {
	s.addCh <- c
}

func (s *Server) Del(c *Client) {
	s.delCh <- c
}

func (s *Server) SendAll(msg *JSONMessage) {
	s.sendAllCh <- msg
}

func (s *Server) Done() {
	s.doneCh <- true
}

func (s *Server) Err(err error) {
	s.errCh <- err
}

func (s *Server) AppendNewMessage(client *Client, msg *Message) {
	sessid := *client.GetSessionId()
	s.clients[sessid].history = append(s.clients[sessid].history, msg)
}

func (s *Server) SendMatrixMessage(c *Client, msg JSONMessage) {
	var r mid.RoomID
	r = mid.RoomID(*c.session.RoomID)
	log.Printf("message: %s\n RoomID: %s ", msg.String(), *c.session.RoomID)
	content := format.RenderMarkdown(msg.Body, true, true)
	s.Mautrix_client.SendMessage(r, &content)
}

func (s *Server) sendPastMessages(c *Client) {
	log.Println("Sending old messages from session: ")
	for _, msg := range s.clients[*c.session.SessionId].history {
		c.Write(&JSONMessage{Author: *msg.Author, Body: *msg.Body})
	}
}

// Broadcasts messages to all websockets from the same client . If bool is true
// then it will include the client, for example it should not be set to true if
// you want to broadcast a message from socket A, but not to self.
func (s *Server) Broadcast(c *Client, message *JSONMessage, exclude_self bool) {
	for _, client_id := range s.clients[*c.session.SessionId].clients {
		if !exclude_self || client_id.id != c.id {
			client_id.Write(message)
		}
	}
}

func (s *Server) sendAll(msg *JSONMessage) {
	for _, c := range s.clients {
		for _, c := range c.clients {
			c.Write(msg)
		}
	}
}

// Trying to access the original request before it upgrades the http connection
// to a websocket one. Use this to apply any middlewares, as for authentication
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tokenCookie, err := r.Cookie("session_id")
	if err != nil {
		log.Panic("No session cookie, abort!")
	}

	// websocket handler
	onConnected := func(ws *websocket.Conn) {
		defer func() {
			err := ws.Close()
			if err != nil {
				s.errCh <- err
			}
		}()

		err, client := NewClient(ws, s, tokenCookie)
		if err != nil {
			err := ws.Close()
			if err != nil {
				s.errCh <- err
			}
		} else {
			s.Add(client)
			client.Listen()
		}

	}
	handler := websocket.Handler(onConnected)
	handler.ServeHTTP(w, r)
}

// Listen and serve.
// It serves client connection and broadcast request.
func (s *Server) Listen() {
	session := NewSession(nil, nil)
	http.Handle("/session", session)
	http.Handle(s.pattern, s)

	for {
		select {

		// Add new a client
		case c := <-s.addCh:
			if nclient := s.clients[*c.session.SessionId]; nclient == nil {
				s.clients[*c.session.SessionId] = NewClientIndex()
			}
			s.sendPastMessages(c)
			s.clients[*c.session.SessionId].clients[c.id] = c
			log.Printf("Added new Client: %s, total clients now: (%d)", *c.session.SessionId, len(s.clients))
			if rid := c.GetRoomId(); *rid != "" {
				s.Mautrix_client.JoinRoomByID(mid.RoomID(*rid))
			} else {
				roomid, err := s.Mautrix_client.CreateRoom(c, s.encrypted)
				if err != nil {
					log.Println("Could not create room, abort!")
				} else {
					*c.session.RoomID = string(roomid)
					DB.UpdateRow(c.session)
				}
			}

		// del a client
		case c := <-s.delCh:
			if len(s.clients[*c.session.SessionId].clients) == 0 {
				delete(s.clients, *c.session.SessionId)
			} else {
				delete(s.clients[*c.session.SessionId].clients, c.id)
			}

		// broadcast message for all clients
		case msg := <-s.sendAllCh:
			s.messages = append(s.messages, msg)
			//s.sendAll(msg)

		// listens to matrix events
		case matrix_evt := <-s.Mautrix_client.Ch:
			client, err := s.FindClientByRoomID(matrix_evt.RoomID)
			// Many matrix events are not relevant
			if err == nil {
				jsonmsg := NewJSONMessage(matrix_evt.Content.Raw["body"].(string), "0")
				client.server.Broadcast(&client, jsonmsg, false)
				client.server.clients[*client.session.SessionId].history = append(client.server.clients[*client.session.SessionId].history, NewMessage(&jsonmsg.Author, &jsonmsg.Body))
			}

		case err := <-s.errCh:
			log.Println("Error:", err.Error())

		case <-s.doneCh:
			return
		}
	}
}
