package chat

import (
	"database/sql"
	"encoding/json"

	log "github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	mevent "maunium.net/go/mautrix/event"
	mid "maunium.net/go/mautrix/id"
)

func (store *StateStore) SaveFilterID(userID mid.UserID, filterID string) {
	log.Debug("Upserting row into user_filter_ids")
	tx, err := store.DB.Begin()
	if err != nil {
		tx.Rollback()
		return
	}

	update := "UPDATE user_filter_ids SET filter_id = ? WHERE user_id = ?"
	if _, err := tx.Exec(update, filterID, userID); err != nil {
		tx.Rollback()
		return
	}

	insert := "INSERT OR IGNORE INTO user_filter_ids VALUES (?, ?)"
	if _, err := tx.Exec(insert, userID, filterID); err != nil {
		tx.Rollback()
		return
	}

	tx.Commit()
}

func (store *StateStore) LoadFilterID(userID mid.UserID) string {
	row := store.DB.QueryRow("SELECT filter_id FROM user_filter_ids WHERE user_id = ?", userID)
	var filterID string
	if err := row.Scan(&filterID); err != nil {
		return ""
	}
	return filterID
}

func (store *StateStore) SaveNextBatch(userID mid.UserID, nextBatchToken string) {
	log.Debug("Upserting row into user_batch_tokens")
	tx, err := store.DB.Begin()
	if err != nil {
		tx.Rollback()
		return
	}

	update := "UPDATE user_batch_tokens SET next_batch_token = ? WHERE user_id = ?"
	if _, err := tx.Exec(update, nextBatchToken, userID); err != nil {
		tx.Rollback()
		return
	}

	insert := "INSERT OR IGNORE INTO user_batch_tokens VALUES (?, ?)"
	if _, err := tx.Exec(insert, userID, nextBatchToken); err != nil {
		tx.Rollback()
		return
	}

	tx.Commit()
}

func (store *StateStore) LoadNextBatch(userID mid.UserID) string {
	row := store.DB.QueryRow("SELECT next_batch_token FROM user_batch_tokens WHERE user_id = ?", userID)
	var batchToken string
	if err := row.Scan(&batchToken); err != nil {
		return ""
	}
	return batchToken
}

func (store *StateStore) GetRoomMembers(roomId mid.RoomID) []mid.UserID {
	rows, err := store.DB.Query("SELECT user_id FROM room_members WHERE room_id = ?", roomId)
	users := make([]mid.UserID, 0)
	if err != nil {
		return users
	}
	defer rows.Close()

	var userId mid.UserID
	for rows.Next() {
		if err := rows.Scan(&userId); err == nil {
			users = append(users, userId)
		}
	}
	return users
}

func (store *StateStore) SaveRoom(room *mautrix.Room) {
	// This isn't really used at all.
}

func (store *StateStore) LoadRoom(roomId mid.RoomID) *mautrix.Room {
	// This isn't really used at all.
	return mautrix.NewRoom(roomId)
}

// Crypto related interfaces:

// IsEncrypted returns whether a room is encrypted.
func (store *StateStore) IsEncrypted(roomID mid.RoomID) bool {
	return store.GetEncryptionEvent(roomID) != nil
}

func (store *StateStore) GetEncryptionEvent(roomId mid.RoomID) *mevent.EncryptionEventContent {
	row := store.DB.QueryRow("SELECT encryption_event FROM rooms WHERE room_id = ?", roomId)

	var encryptionEventJson []byte
	if err := row.Scan(&encryptionEventJson); err != nil {
		if err != sql.ErrNoRows {
			log.Errorf("Failed to find encryption event JSON: %s. Error: %s", encryptionEventJson, err)
			return nil
		}
	}
	var encryptionEvent mevent.EncryptionEventContent
	if err := json.Unmarshal(encryptionEventJson, &encryptionEvent); err != nil {
		log.Errorf("Failed to unmarshal encryption event JSON: %s. Error: %s", encryptionEventJson, err)
		return nil
	}
	return &encryptionEvent
}

func (store *StateStore) FindSharedRooms(userId mid.UserID) []mid.RoomID {
	rows, err := store.DB.Query("SELECT room_id FROM room_members WHERE user_id = ?", userId)
	rooms := make([]mid.RoomID, 0)
	if err != nil {
		return rooms
	}
	defer rows.Close()

	var roomId mid.RoomID
	for rows.Next() {
		if err := rows.Scan(&roomId); err != nil {
			rooms = append(rooms, roomId)
		}
	}
	return rooms
}

func (store *StateStore) SetMembership(event *mevent.Event) {
	log.Debugf("Updating room_members for %s", event.RoomID)
	tx, err := store.DB.Begin()
	if err != nil {
		tx.Rollback()
		return
	}
	membershipEvent := event.Content.AsMember()
	if membershipEvent.Membership.IsInviteOrJoin() {
		insert := "INSERT OR IGNORE INTO room_members VALUES (?, ?)"
		if _, err := tx.Exec(insert, event.RoomID, event.GetStateKey()); err != nil {
			log.Errorf("Failed to insert membership row for %s in %s", event.GetStateKey(), event.RoomID)
		}
	} else {
		del := "DELETE FROM room_members WHERE room_id = ? AND user_id = ?"
		if _, err := tx.Exec(del, event.RoomID, event.GetStateKey()); err != nil {
			log.Errorf("Failed to delete membership row for %s in %s", event.GetStateKey(), event.RoomID)
		}
	}
	tx.Commit()
}

func (store *StateStore) upsertEncryptionEvent(roomId mid.RoomID, encryptionEvent *mevent.Event) error {
	tx, err := store.DB.Begin()
	if err != nil {
		tx.Rollback()
		return nil
	}

	update := "UPDATE rooms SET encryption_event = ? WHERE room_id = ?"
	var encryptionEventJson []byte
	if encryptionEvent == nil {
		encryptionEventJson = nil
	}
	encryptionEventJson, err = json.Marshal(encryptionEvent)
	if err != nil {
		encryptionEventJson = nil
	}

	if _, err := tx.Exec(update, encryptionEventJson, roomId); err != nil {
		tx.Rollback()
		return err
	}

	insert := "INSERT OR IGNORE INTO rooms VALUES (?, ?)"
	if _, err := tx.Exec(insert, roomId, encryptionEventJson); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (store *StateStore) SetEncryptionEvent(event *mevent.Event) {
	log.Debugf("Updating encryption_event for %s", event.RoomID)
	tx, err := store.DB.Begin()
	if err != nil {
		tx.Rollback()
		return
	}
	err = store.upsertEncryptionEvent(event.RoomID, event)
	if err != nil {
		log.Errorf("Upsert encryption event failed %s", err)
	}

	tx.Commit()
}
