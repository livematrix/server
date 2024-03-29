package chat

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"golang.org/x/net/websocket"
)

const channelBufSize = 100

var maxId int = 0

type Client struct {
	id      int
	ws      *websocket.Conn
	server  *Server
	ch      chan *JSONMessage
	doneCh  chan bool
	session *Session
}

func NewClient(ws *websocket.Conn, server *Server, token *http.Cookie) (error, *Client) {
	if ws == nil {
		panic("ws cannot be nil")
	}
	if server == nil {
		panic("server cannot be nil")
	}

	maxId++
	ch := make(chan *JSONMessage, channelBufSize)
	doneCh := make(chan bool)
	session := NewSession(nil, nil)
	err := DB.GetByPk(session, token.Value, "session")
	if err != nil {
		log.Println(err)
		return err, nil
	}

	return nil, &Client{maxId, ws, server, ch, doneCh, session}
}

func (c *Client) GetRoomId() *string {
	return c.session.GetRoomId()
}

func (c *Client) GetSessionId() *string {
	return c.session.GetSessionID()
}

func (c *Client) Conn() *websocket.Conn {
	return c.ws
}

func (c *Client) Write(msg *JSONMessage) {
	select {
	case c.ch <- msg:
	default:
		c.server.Del(c)
		err := fmt.Errorf("client %d is disconnected.", c.id)
		c.server.Err(err)
	}
}

func (c *Client) Done() {
	c.doneCh <- true
}

func (c *Client) Listen() {
	go c.listenWrite()
	c.listenRead()
}

func (c *Client) AppendNewMessage(msg *Message) {
	c.server.AppendNewMessage(c, msg)
}

func (c *Client) GetJSONMessages() []*JSONMessage {
	var jsonlist []*JSONMessage
	messages := c.session.Messages
	if messages != nil {
		for _, msg := range *c.session.Messages {
			jsonlist = append(jsonlist, &JSONMessage{Author: *msg.Author, Body: *msg.Body})
		}
	}
	return jsonlist
}

func (c *Client) listenWrite() {
	for {
		select {

		// send message to the client
		case msg := <-c.ch:
			websocket.JSON.Send(c.ws, msg)

		// receive done request
		case <-c.doneCh:
			c.server.Del(c)
			c.doneCh <- true // for listenRead method
			return
		}
	}
}

func (c *Client) listenRead() {
	for {
		select {

		// receive done request
		case <-c.doneCh:
			c.server.Del(c)
			c.doneCh <- true // for listenWrite method
			return

		// read data from websocket connection
		default:
			var msg JSONMessage
			err := websocket.JSON.Receive(c.ws, &msg)
			if err == io.EOF {
				c.doneCh <- true
			} else if err != nil {
				c.server.Err(err)
			} else {
				c.AppendNewMessage(NewMessage(&msg.Author, &msg.Body))
				//broadcasting to same client sockets, excluding self:
				c.server.Broadcast(c, &msg, true)
				c.server.SendMatrixMessage(c, msg)
			}
		}
	}
}
