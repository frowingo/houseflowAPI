package appEvents

import (
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	fiberWebsocket "github.com/gofiber/contrib/websocket"
)

var readyMessage = []byte(`{"type":"ready"}`)

type client struct {
	socket    *fiberWebsocket.Conn
	outbound  chan []byte
	closeOnce sync.Once
}

func (connection *client) close() {
	connection.closeOnce.Do(func() {
		if connection.socket != nil {
			_ = connection.socket.Close()
		}
	})
}

func (service *Service) handleConnection(socket *fiberWebsocket.Conn) {
	userID, _ := socket.Locals("appEventUserID").(string)
	expiresAt, _ := socket.Locals("tokenExpiresAt").(time.Time)
	if userID == "" || !time.Now().Before(expiresAt) {
		_ = socket.Close()
		return
	}
	connection := &client{socket: socket, outbound: make(chan []byte, queueSize)}
	service.mutex.Lock()
	count := 0
	for _, group := range service.clients {
		count += len(group)
	}
	if !service.online || service.closed || count >= maxConnections || len(service.clients[userID]) >= maxConnectionsPerUser {
		service.mutex.Unlock()
		connection.close()
		return
	}
	if service.clients[userID] == nil {
		service.clients[userID] = make(map[*client]struct{})
	}
	service.clients[userID][connection] = struct{}{}
	service.mutex.Unlock()
	defer func() {
		connection.close()
		service.mutex.Lock()
		delete(service.clients[userID], connection)
		if len(service.clients[userID]) == 0 {
			delete(service.clients, userID)
		}
		service.mutex.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		connection.writeLoop(expiresAt)
	}()
	connection.readLoop(expiresAt)
	connection.close()
	<-done
}

func (connection *client) readLoop(expiresAt time.Time) {
	connection.socket.SetReadLimit(1024)
	_ = connection.socket.SetReadDeadline(minTime(time.Now().Add(pongTimeout), expiresAt))
	connection.socket.SetPongHandler(func(string) error {
		return connection.socket.SetReadDeadline(minTime(time.Now().Add(pongTimeout), expiresAt))
	})
	for {
		if _, _, err := connection.socket.ReadMessage(); err != nil {
			return
		}
	}
}

func (connection *client) writeLoop(expiresAt time.Time) {
	defer connection.close()
	if err := connection.write(websocket.TextMessage, readyMessage); err != nil {
		return
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	expiry := time.NewTimer(time.Until(expiresAt))
	defer expiry.Stop()
	for {
		select {
		case message := <-connection.outbound:
			if err := connection.write(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ping.C:
			if err := connection.write(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-expiry.C:
			return
		}
	}
}

func (connection *client) write(kind int, message []byte) error {
	if err := connection.socket.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return connection.socket.WriteMessage(kind, message)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
