telegram-nncp-bot
=================

Relay messages from Telegram over NNCP in a maildir like fashion. When invited to a new chat, this bot will begin relaying all received messages over NNCP. Each message is stored in a file whose filename is constructed in a Maildir like fashion.

## Known Limitations
Currently if multiple messages with the same timestamp are relayed there is no way for the receiving end to order these messages properly. It should be fairly simple to fix this, but for now this happens fairly infrequently.
