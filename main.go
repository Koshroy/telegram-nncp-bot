package main

import (
	"database/sql"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
)

type MsgStatus int
const (
	MsgUnsent = iota
	MsgFailed
	MsgSent
)

type Message struct {
	Timestamp string
	ChatId int64
	Username string
	Contents string
}

func tgToSqlMsg(ts int, username string, chatId int64, contents string) Message {
	isoTs := time.Unix(int64(ts), 0).Format(time.RFC3339)
	return Message{
		Timestamp: isoTs,
		ChatId: chatId,
		Username: username,
		Contents: contents,
	}
}

func main() {
	initFlag := flag.Bool("init", false, "initialize the database")
	flag.Parse()

	db, err := sql.Open("sqlite3", "./messages.db")
	if err != nil {
		log.Fatalf("could not open sqlite db: %v\n", err)
	}

	if *initFlag {
		log.Printf("Initializing database")
		sqlStmt := `
                CREATE TABLE messages (timestamp TEXT, chat_id INTEGER, username TEXT, contents TEXT);
                DELETE FROM messages;
                `
		_, err := db.Exec(sqlStmt)
		if err != nil {
			log.Fatalf("could not initialize sqlite database: %v\n", err)
		}
		return
	}

	// We do not need to distinguish between an empty string envar and a not present envar here
	apiKey := os.Getenv("TG_BOT_SECRET")
	if apiKey == "" {
		log.Fatalln("Need to set the Telegram bot secret in envar TG_BOT_SECRET, got empty string")
	}
	bot, err := tgbotapi.NewBotAPI(apiKey)
	if err != nil {
		log.Fatalf("error connecting to bot api: %v\n", err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Fatalf("Could not create Telegram update channel: %v\n", err)
	}

	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			//log.Printf("Raw message: %v\n", update)
			continue
		}

		var ts int
		if update.Message.EditDate != 0 {
			ts = update.Message.EditDate
		} else {
			ts = update.Message.Date
		}
		
		dbMsg := tgToSqlMsg(ts, update.Message.From.UserName, update.Message.Chat.ID, update.Message.Text)
		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
		err := addMsg(db, &dbMsg)
		if err != nil {
			log.Printf("Error inserting message to database: %v\n", err)
		}

		msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
		msg.ReplyToMessageID = update.Message.MessageID

		bot.Send(msg)
	}
}

func addMsg(db *sql.DB, msg *Message) error {
	if msg.Timestamp == "" || msg.Username == "" || msg.Contents == "" || msg.ChatId == 0 {
		return errors.New("message timestamp, chat id, username, and contents must be provided")
	}

	insertStmt := "INSERT INTO messages(timestamp, chat_id, username, contents) VALUES(?, ?, ?, ?)"

	_, err := db.Exec(insertStmt, msg.Timestamp, msg.ChatId, msg.Username, msg.Contents)
	return err
}
