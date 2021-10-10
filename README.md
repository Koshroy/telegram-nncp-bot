telegram-nncp-bot
=================

Relay messages from Telegram over NNCP in a maildir like fashion. When invited to a new chat, this bot will begin relaying all received messages over NNCP. Each message is stored in a file whose filename is constructed in a Maildir like fashion.

## Usage

Initialize the database:
```
telegram-nncp-bot -init -db <dbpath>
```

Run the bot:
```
telegram-nncp-bot -db <dbpath>
```

Make sure to set the environment varables if needed:

1. `TG_BOT_SECRET` for the Telegram Bot secret (always needed).
2. `NNCP_PATH` for the path to `nncp-file` if it is not already in your path.
2. `NNCP_CFG_PATH` for the path to your NNCP node config if it's not at the default path.

Further flags are available by running `telegram-nncp-bot -h`

## How does it work?
Messages are received from Telegram and then placed in a SQLite database. A goroutine regularly runs a query against this SQLite database, finds unrelayed messages, and relays each message over NNCP. A SQLite database was used for a couple reasons:

1. Auditability. It's easy to see if any messages were unsent by looking at the SQLite database. Messages that failed to sent are also recorded.
2. Resiliance. If the bot has to shut down for any reason (e.g. caused by a system shutdown), then on restart, unsent messages will be sent by the bot.
3. Potential extensibility. If the architecture ever changes, I'll be able to use the SQLite rows to bootstrap a new architecture instead of being tied to a struct sent over a Goroutine.

## Known Limitations
Currently if multiple messages with the same timestamp are relayed there is no way for the receiving end to order these messages properly. It should be fairly simple to fix this, but for now this happens fairly infrequently.
