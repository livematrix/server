package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Session struct {
	Id        int     `db:"id"`
	SessionId *string `db:"session"`
	Expirity  *string `db:"expirity"`
	Alias     *string `db:"alias"`
	Email     *string `db:"email"`
	IpAddr    *string `db:"ip"`
	RoomID    *string `db:"RoomID"`
	Messages  *[]Message
	mutex     *sync.Mutex
}

func NewSession(msgs *[]Message, sess_id []byte, args ...*string) *Session {
	if len(args) == 0 {
		return &Session{
			0,
			new(string),
			new(string),
			new(string),
			new(string),
			new(string),
			new(string),
			msgs,
			new(sync.Mutex),
		}
	} else if len(args) == 5 {
		return &Session{
			0,
			args[0], //SessionId
			args[1], //Expirity
			args[2], //Alias
			args[3], //Email
			args[4], //IpAddr
			args[5], //IpAddr
			msgs,
			new(sync.Mutex),
		}
	}
	return nil
}

func (e *Session) GetRoomId() *string {
	return e.RoomID
}

func (e *Session) GetById(id int) error {
	tmp := NewSession(nil, nil)
	err := DB.GetById(tmp, id)
	if err != nil {
		return err
	}
	*e = *tmp
	return nil
}

func Hash254(args ...string) string {
	hash := sha256.New()
	for _, arg := range args {
		hash.Write([]byte(arg))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

//
//
//
func (s *Session) createCookie(name, domain string) *http.Cookie {
	m := regexp.MustCompile(`\.?([^.]*.[a-z]{0,4})$`)
	format := "2006-01-02 15:04:05 -0700"
	if len(m.FindStringSubmatch(domain)) > 1 {
		domain = "." + m.FindStringSubmatch(domain)[1]
	}
	*s.Expirity = time.Now().Add(365 * 24 * time.Hour).Format(format)
	*s.SessionId = Hash254(strconv.Itoa(rand.Intn(128000)) + *s.Expirity)
	time, _ := time.Parse(format, *s.Expirity)
	fmt.Println()
	return &http.Cookie{
		Value:   *s.SessionId,
		Name:    "session_id",
		Domain:  domain,
		Expires: time,
	}
}

//
//
//
func (s *Session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Please pass the data as URL form encoded", http.StatusBadRequest)
		return
	}
	log.Println(r.RemoteAddr)
	tokenCookie, err := r.Cookie("session_id")
	if err != nil {
		name := r.PostForm.Get("name")
		surname := r.PostForm.Get("surname")
		*s.IpAddr = r.RemoteAddr[:strings.LastIndex(r.RemoteAddr, ":")]
		*s.Email = r.PostForm.Get("email")
		*s.Alias = name + "_" + surname
		log.Printf("raw r.Host: %s", r.Host)
		cookie := s.createCookie("session_id", r.Host)
		log.Printf("domain inside cookie: %s", cookie.Domain)
		http.SetCookie(w, cookie)
		*s.SessionId = cookie.Value
		DB.InsertRow(s)
	} else {
		log.Println(tokenCookie.Value)
	}
}
