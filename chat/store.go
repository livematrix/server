package chat

import (
	"database/sql"
	"log"

	"maunium.net/go/mautrix"
)

type StateStore struct {
	DB     *sql.DB
	Client *mautrix.Client
}

func NewStateStore(db *sql.DB) *StateStore {
	return &StateStore{DB: db}
}

func (store *StateStore) CreateTables() error {
	log.Println("Creating databases and syncing with Matrix...")
	tx, err := store.DB.Begin()
	if err != nil {
		return err
	}

	queries := []string{
		`CREATE TABLE if not exists Session(
				id INTEGER PRIMARY KEY ,
			  	session varchar(100) NOT NULL,
			  	expirity varchar(100) DEFAULT NULL,
			  	alias varchar(100) DEFAULT NULL,
			  	email varchar(100) DEFAULT NULL,
			  	ip varchar(100) DEFAULT NULL,
			  	RoomID varchar(256) DEFAULT NULL
			  )
		`,
		`
		CREATE TABLE if not exists Matrix(
			    token varchar(100) NOT NULL,
			    userid varchar(100) NOT NULL,
			    created datetime DEFAULT NULL,
			    PRIMARY KEY (userid)
			  ) 
		`,
		`
		CREATE TABLE IF NOT EXISTS user_filter_ids (
			user_id    VARCHAR(255) PRIMARY KEY,
			filter_id  VARCHAR(255)
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS user_batch_tokens (
			user_id           VARCHAR(255) PRIMARY KEY,
			next_batch_token  VARCHAR(255)
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS rooms (
			room_id           VARCHAR(255) PRIMARY KEY,
			encryption_event  TEXT NULL
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS room_members (
			room_id  VARCHAR(255),
			user_id  VARCHAR(255),
			PRIMARY KEY (room_id, user_id)
		)
		`,
	}

	for _, query := range queries {
		if _, err := tx.Exec(query); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}
