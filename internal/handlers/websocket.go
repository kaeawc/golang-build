package handlers

import (
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/kaeawc/golang-build/internal/logger"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

func WebSocket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log := logger.FromContext(r.Context())
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Warn("websocket upgrade", "err", err)
			return
		}
		defer conn.Close()
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Warn("websocket read", "err", err)
				}
				break
			}
			if err := conn.WriteMessage(msgType, msg); err != nil {
				log.Warn("websocket write", "err", err)
				break
			}
		}
	}
}
