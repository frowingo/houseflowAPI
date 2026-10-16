package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	houseRockets "houseflowApi/internal/application/game/games/houseRockets"
	"houseflowApi/internal/infrastructure/realtime"

	fasthttpWebsocket "github.com/fasthttp/websocket"
)

// countingConn counts the bytes a client reads off the network, before any
// decompression.
type countingConn struct {
	net.Conn
	read *atomic.Int64
}

func (conn countingConn) Read(buffer []byte) (int, error) {
	count, err := conn.Conn.Read(buffer)
	conn.read.Add(int64(count))
	return count, err
}

type measuredSocket struct {
	connection *fasthttpWebsocket.Conn
	extensions string
	wireBytes  atomic.Int64
}

func dialMeasured(t *testing.T, endpoint, token string, compress bool) *measuredSocket {
	t.Helper()
	socket := &measuredSocket{}
	dialer := fasthttpWebsocket.Dialer{
		EnableCompression: compress,
		HandshakeTimeout:  5 * time.Second,
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return countingConn{Conn: conn, read: &socket.wireBytes}, nil
		},
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	connection, response, err := dialer.Dial(endpoint, header)
	if err != nil {
		t.Fatalf("dial: %v status=%v", err, responseStatus(response))
	}
	t.Cleanup(func() { _ = connection.Close() })
	socket.connection, socket.extensions = connection, response.Header.Get("Sec-WebSocket-Extensions")
	return socket
}

// snapshots reads for a while and returns how many game snapshots arrived and
// the wire bytes spent on that period. It runs on its own goroutine, so it
// reports failures instead of stopping the test.
func (socket *measuredSocket) snapshots(duration time.Duration) (int, int64, error) {
	start := socket.wireBytes.Load()
	_ = socket.connection.SetReadDeadline(time.Now().Add(duration))
	count := 0
	for {
		_, payload, err := socket.connection.ReadMessage()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return count, socket.wireBytes.Load() - start, nil
			}
			return 0, 0, err
		}
		var message realtime.ServerMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return 0, 0, err
		}
		if message.Type == houseRockets.SnapshotMessageType {
			count++
		}
	}
}

// A client that offers permessage-deflate gets compressed snapshots with the
// same content; a client that does not is served exactly as before.
func TestRealtimeCompressionIsNegotiatedPerConnection(t *testing.T) {
	fixture := newRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := persistRocketsSession(t, fixture.gameSessionApplicationFixture, true)
	endpoint := "ws" + fixture.urls[0][4:] + "/api/v1/game/" + session.SessionID + "/realtime?protocolVersion=2"
	compressed := dialMeasured(t, endpoint, generateGatewayToken(t, fixture.jwt, fixture.ownerID), true)
	plain := dialMeasured(t, endpoint, generateGatewayToken(t, fixture.jwt, fixture.memberID), false)
	if !strings.Contains(compressed.extensions, "permessage-deflate") {
		t.Fatalf("compression not negotiated: %q", compressed.extensions)
	}
	if plain.extensions != "" {
		t.Fatalf("compression forced on a client that did not offer it: %q", plain.extensions)
	}

	// Both sockets read the same frames over the same period.
	type measurement struct {
		snapshots int
		bytes     int64
		err       error
	}
	results := make([]chan measurement, 2)
	for index, socket := range []*measuredSocket{compressed, plain} {
		results[index] = make(chan measurement, 1)
		go func(socket *measuredSocket, result chan<- measurement) {
			count, bytes, err := socket.snapshots(2 * time.Second)
			result <- measurement{count, bytes, err}
		}(socket, results[index])
	}
	compressedResult, plainResult := <-results[0], <-results[1]
	if compressedResult.err != nil || plainResult.err != nil {
		t.Fatalf("read: compressed %v, plain %v", compressedResult.err, plainResult.err)
	}
	compressedCount, compressedBytes := compressedResult.snapshots, compressedResult.bytes
	plainCount, plainBytes := plainResult.snapshots, plainResult.bytes
	if compressedCount < 20 || plainCount < 20 {
		t.Fatalf("snapshots received: compressed %d, plain %d", compressedCount, plainCount)
	}
	perCompressed := float64(compressedBytes) / float64(compressedCount)
	perPlain := float64(plainBytes) / float64(plainCount)
	t.Logf("wire bytes per snapshot: compressed %.0f, plain %.0f (%.0f%% smaller)", perCompressed, perPlain, 100*(1-perCompressed/perPlain))
	// Two-player snapshots are small; eight-player ones shrink about 75%.
	if perCompressed > perPlain*0.6 {
		t.Fatalf("compression saved too little: %.0f vs %.0f bytes per snapshot", perCompressed, perPlain)
	}
}
