package main

import (
	"bytes"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/google/uuid"
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
	ChatId    int64
	Username  string
	Contents  string
	Status    MsgStatus
}

type MsgDbUpdate struct {
	Rowid  int
	Status MsgStatus
}

func tgToSqlMsg(ts int, username string, chatId int64, contents string) Message {
	isoTs := time.Unix(int64(ts), 0).Format(time.RFC3339)
	return Message{
		Timestamp: isoTs,
		ChatId:    chatId,
		Username:  username,
		Contents:  contents,
		Status:    MsgUnsent,
	}
}

func main() {
	initFlag := flag.Bool("init", false, "initialize the database")
	dryRun := flag.Bool("dryrun", false, "do not actually run nncp")
	debug := flag.Bool("debug", false, "enable debug logging")
	botDebug := flag.Bool("botdebug", false, "enable debug logging for the telegram bot")
	dbPath := flag.String("db", "./messages.db", "path to messages database")
	flag.Parse()

	if *dryRun {
		log.Println("Running in dry run mode, so not invoking nncp")
	}

	if *dbPath == "" {
		log.Fatalln("path to db was not provided")
	}

	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("could not open sqlite db: %v\n", err)
	}

	if *initFlag {
		log.Printf("Initializing database")
		sqlStmt := `
                CREATE TABLE messages (timestamp TEXT, chat_id INTEGER, username TEXT, contents TEXT, status INTEGER);
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
	nncpPath := os.Getenv("NNCP_PATH")
	if nncpPath == "" {
		nncpPath = "nncp-file"
	} else {
		absPath, err := filepath.Abs(nncpPath)
		if err == nil {
			nncpPath = absPath
		} else {
			if *debug {
				log.Printf("error canonicalizing nncp-file path: %v\n", err)
			}
		}

	}
	nncpCfgPath := os.Getenv("NNCP_CFG_PATH")
	if nncpCfgPath != "" {
		absPath, err := filepath.Abs(nncpCfgPath)
		if err == nil {
			nncpCfgPath = absPath
		} else {
			if *debug {
				log.Printf("error canonicalizing config path: %v\n", err)
			}
		}
	}
	destNode := flag.Arg(0)
	if destNode == "" && !*dryRun {
		log.Fatalln("Need a destination node to send messages to")
	}

	if !*dryRun {
		go nncpLoop(nncpPath, nncpCfgPath, db, destNode, *debug)
	}

	if *debug {
		log.Println("Running with debug output")
		log.Println("nncp path:", nncpPath)
		log.Println("config path:", nncpCfgPath)
		log.Println("destination node:", destNode)
	}

	bot, err := tgbotapi.NewBotAPI(apiKey)
	if err != nil {
		log.Fatalf("error connecting to bot api: %v\n", err)
	}

	if *botDebug {
		bot.Debug = true
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)

	welcomeSet := make(map[int64]bool)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Fatalf("Could not create Telegram update channel: %v\n", err)
	}

	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		if _, ok := welcomeSet[update.Message.Chat.ID]; !ok {
			if *debug {
				log.Println("encountered previously unseen chat id:", update.Message.Chat.ID)
			}
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Hi! I'm an NNCP relay bot!")
			bot.Send(msg)
			welcomeSet[update.Message.Chat.ID] = true
		}

		var ts int
		if update.Message.EditDate != 0 {
			ts = update.Message.EditDate
		} else {
			ts = update.Message.Date
		}
		userName := update.Message.From.UserName
		if userName == "" {
			userName = update.Message.From.FirstName + update.Message.From.LastName
		}
		chatID := update.Message.Chat.ID
		text := update.Message.Text

		dbMsg := tgToSqlMsg(ts, userName, chatID, text)
		log.Printf("#%d - [%s] %s", chatID, userName, text)
		err := addMsg(db, &dbMsg)
		if err != nil {
			log.Printf("Error inserting message to database: %v\n", err)
		}
	}
}

func addMsg(db *sql.DB, msg *Message) error {
	if msg.Timestamp == "" || msg.Username == "" || msg.Contents == "" || msg.ChatId == 0 {
		return errors.New("message timestamp, chat id, username, and contents must be provided")
	}

	insertStmt := "INSERT INTO messages(timestamp, chat_id, username, contents, status) VALUES(?, ?, ?, ?, ?)"

	_, err := db.Exec(insertStmt, msg.Timestamp, msg.ChatId, msg.Username, msg.Contents, msg.Status)
	return err
}

func changeMsgStatus(db *sql.DB, rowid int, status MsgStatus) error {
	stmt, err := db.Prepare("UPDATE messages SET status = ? WHERE rowid = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(status, rowid)
	if err != nil {
		return err
	}

	return nil
}

func nncpLoop(nncpPath string, nncpCfgPath string, db *sql.DB, destNode string, debug bool) {
	for {
		var payloadBuf bytes.Buffer
		var destBuf strings.Builder

		var stdoutBuf bytes.Buffer

		<-time.After(2 * time.Second)

		// We need to release resources on every loop iteration
		// So wrap this block in a function and execute the function
		// on each loop iteration. The "defer" calls will release resources
		// on function return
		func() {
			stmt, err := db.Prepare(
				"SELECT rowid, timestamp, chat_id, username, contents FROM messages WHERE status = ?",
			)
			if err != nil {
				log.Printf("could not prepare SQL to query message db: %v\n", err)
				return
			}
			defer stmt.Close()
			rows, err := stmt.Query(MsgUnsent)
			if err != nil {
				log.Printf("error querying for unsent messages: %v\n", err)
				return
			}

			updates := make([]MsgDbUpdate, 0)

			for rows.Next() {
				// We should never return from this function
				// as rows.Close() must be called after this loop
				var rowid int
				var ts string
				var chatId int
				var username string
				var contents string

				err = rows.Scan(&rowid, &ts, &chatId, &username, &contents)
				if err != nil {
					// If we cannot unmarshal the SQLite row, log the error
					// and move onto the next row
					log.Printf("error hydrating sqlite db row: %v\n", err)
					continue
				}

				log.Printf("Relaying message ID %d\n", rowid)
				fmt.Fprintf(&payloadBuf, "[%s] <%s> %s\n", ts, username, contents)

				// Use positive values only for constructing filepaths
				if chatId < 0 {
					chatId = -chatId
				}
				chatUuid, err := uuid.NewRandom()

				// destNode: tgchat/%d/%s-%s.txt (chatid, ts, uuid)
				destBuf.WriteString(destNode)
				destBuf.WriteRune(':')
				destBuf.WriteString("tgchat/")
				destBuf.WriteString(strconv.Itoa(chatId))
				destBuf.WriteRune('/')
				destBuf.WriteString(ts)
				if err == nil {
					uuidBytes, err := chatUuid.MarshalText()
					if err == nil {
						destBuf.WriteRune('-')
						destBuf.Write(uuidBytes)
					} else {
						log.Printf("error marshalling uuid bytes: %v\n", err)
					}
				} else {
					log.Printf("error generating uuid: %v\n", err)
				}
				destBuf.WriteString(".txt")

				if debug {
					log.Println("destBuf:", destBuf.String())
				}

				var cmd *exec.Cmd
				if nncpCfgPath == "" {
					cmd = exec.Command(nncpPath, "-", destBuf.String())
				} else {
					cmd = exec.Command(nncpPath, "-cfg", nncpCfgPath, "-", destBuf.String())
				}

				cmd.Stdin = &payloadBuf
				if debug {
					cmd.Stdout = &stdoutBuf
				}

				var newStatus MsgStatus
				err = cmd.Run()
				if err != nil {
					log.Printf("error occurred when running nncp-file: %v\n", err)
					if debug {
						log.Println("stdout: ", stdoutBuf.String())
					}
					newStatus = MsgFailed
				} else {
					newStatus = MsgSent
				}
				updates = append(updates, MsgDbUpdate{rowid, newStatus})

				payloadBuf.Reset()
				destBuf.Reset()
				if debug {
					stdoutBuf.Reset()
				}
			}
			rows.Close()

			for _, update := range updates {
				if debug {
					log.Printf("Updating rowid %d status: %d\n", update.Rowid, update.Status)
				}
				err := changeMsgStatus(db, update.Rowid, update.Status)
				if err != nil {
					log.Printf("Error updating rowid %d status: %v\n", update.Rowid, err)
				}
			}
		}()
	}
}
