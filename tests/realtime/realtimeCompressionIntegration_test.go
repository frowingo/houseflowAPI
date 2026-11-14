package realtime

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
	"houseflowApi/tests/testkit/gamekit"

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
		t.Fatalf("dial: %v status=%v", err, gamekit.ResponseStatus(response))
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

// The read limit counts compressed wire bytes. A message that is small on the
// wire but inflates past the limit must close the socket unread (1009), like
// one that is too big on the wire.
func TestRealtimeCompressedOversizedMessageClosesConnection(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, false)
	endpoint := "ws" + fixture.URLs[0][4:] + "/api/v1/game/" + session.SessionID + "/realtime?protocolVersion=2"
	socket := dialMeasured(t, endpoint, gamekit.GenerateGatewayToken(t, fixture.JWT, fixture.OwnerID), true)
	if !strings.Contains(socket.extensions, "permessage-deflate") {
		t.Fatalf("compression not negotiated: %q", socket.extensions)
	}
	read := func(predicate func(realtime.ServerMessage) bool) error {
		for {
			_, payload, err := socket.connection.ReadMessage()
			if err != nil {
				return err
			}
			var message realtime.ServerMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatal(err)
			}
			if predicate(message) {
				return nil
			}
		}
	}
	_ = socket.connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	// A compressed message within the limit is still served.
	if err := socket.connection.WriteMessage(fasthttpWebsocket.TextMessage, []byte(`{"protocolVersion":2,"messageId":"small","type":"ping","payload":{"pingId":"small"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := read(func(message realtime.ServerMessage) bool { return message.MessageID == "small" }); err != nil {
		t.Fatalf("compressed ping: %v", err)
	}
	inputBefore := fixture.Services[0].Metrics().InputPayloadBytes
	// 1 MiB of one byte deflates to about 1 KiB, far under the 16 KiB wire limit.
	oversized := `{"protocolVersion":2,"messageId":"oversized","type":"ping","payload":{"pingId":"` + strings.Repeat("a", 1<<20) + `"}}`
	if err := socket.connection.WriteMessage(fasthttpWebsocket.TextMessage, []byte(oversized)); err != nil {
		t.Fatal(err)
	}
	err := read(func(message realtime.ServerMessage) bool {
		if message.Type == realtime.CommandRejectedMessageType || message.MessageID == "oversized" {
			t.Fatalf("oversized message was processed: %+v", message)
		}
		return false
	})
	if !fasthttpWebsocket.IsCloseError(err, fasthttpWebsocket.CloseMessageTooBig) {
		t.Fatalf("connection not closed as too big: %v", err)
	}
	if read := fixture.Services[0].Metrics().InputPayloadBytes - inputBefore; read != 0 {
		t.Fatalf("server counted %d bytes of the oversized message", read)
	}
}

// A client that offers permessage-deflate gets compressed snapshots with the
// same content; a client that does not is served exactly as before.
func TestRealtimeCompressionIsNegotiatedPerConnection(t *testing.T) {
	fixture := gamekit.NewRocketsGatewayFixture(t, houseRockets.Definition().Rules)
	session := gamekit.PersistRocketsSession(t, fixture.GameSessionApplicationFixture, true)
	endpoint := "ws" + fixture.URLs[0][4:] + "/api/v1/game/" + session.SessionID + "/realtime?protocolVersion=2"
	compressed := dialMeasured(t, endpoint, gamekit.GenerateGatewayToken(t, fixture.JWT, fixture.OwnerID), true)
	plain := dialMeasured(t, endpoint, gamekit.GenerateGatewayToken(t, fixture.JWT, fixture.MemberID), false)
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
