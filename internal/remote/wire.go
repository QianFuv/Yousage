package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type CallError struct {
	Message          string
	IsOutcomeUnknown bool
	IsAuthorization  bool
}

func (failure *CallError) Error() string { return failure.Message }

type envelope struct {
	Tag  string          `json:"_tag"`
	Id   json.RawMessage `json:"requestId"`
	Exit struct {
		Tag   string          `json:"_tag"`
		Value json.RawMessage `json:"value"`
		Cause json.RawMessage `json:"cause"`
	} `json:"exit"`
	Values []json.RawMessage `json:"values"`
}
type pending struct{ events chan envelope }
type Client struct {
	Environment    Environment
	Authorization  Authorization
	socket         *websocket.Conn
	owner          context.Context
	cancel         context.CancelFunc
	closed         chan struct{}
	mutex          sync.Mutex
	requests       map[string]*pending
	sequence       atomic.Uint64
	isAwaitingPong atomic.Bool
	keepaliveDone  chan struct{}
}

func dial(ctx context.Context, endpoint string, httpClient *http.Client, environment Environment, authorization Authorization) (*Client, error) {
	socket, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		if response != nil && response.StatusCode == 426 {
			return nil, errors.New("environment orchestration protocol is incompatible")
		}
		return nil, errors.New("environment websocket could not connect")
	}
	socket.SetReadLimit(32 << 20)
	owner, cancel := context.WithCancel(context.Background())
	client := &Client{Environment: environment, Authorization: authorization, socket: socket, owner: owner, cancel: cancel, closed: make(chan struct{}), keepaliveDone: make(chan struct{}), requests: map[string]*pending{}}
	go client.read()
	go client.keepalive()
	return client, nil
}
func (client *Client) send(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	write, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return client.socket.Write(write, websocket.MessageText, data)
}
func requestId(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}
func (client *Client) read() {
	defer close(client.closed)
	defer client.cancel()
	defer client.socket.CloseNow()
	for {
		kind, data, err := client.socket.Read(client.owner)
		if err != nil || kind != websocket.MessageText {
			return
		}
		data = bytes.TrimSpace(data)
		frames := []json.RawMessage{}
		if len(data) > 0 && data[0] == '[' {
			if json.Unmarshal(data, &frames) != nil {
				return
			}
		} else {
			frames = append(frames, data)
		}
		for _, frame := range frames {
			var message envelope
			if json.Unmarshal(frame, &message) != nil {
				return
			}
			switch message.Tag {
			case "Ping":
				if client.send(client.owner, map[string]string{"_tag": "Pong"}) != nil {
					return
				}
				continue
			case "Pong":
				client.isAwaitingPong.Store(false)
				continue
			case "Defect":
				return
			case "Exit", "Chunk":
				client.mutex.Lock()
				pending := client.requests[requestId(message.Id)]
				client.mutex.Unlock()
				if pending == nil {
					continue
				}
				select {
				case pending.events <- message:
				default:
					return
				}
			default:
				return
			}
		}
	}
}
func (client *Client) keepalive() {
	defer close(client.keepaliveDone)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-client.owner.Done():
			return
		case <-ticker.C:
			if client.isAwaitingPong.Swap(true) || client.send(client.owner, map[string]string{"_tag": "Ping"}) != nil {
				client.cancel()
				_ = client.socket.CloseNow()
				return
			}
		}
	}
}
func (client *Client) Close() {
	client.cancel()
	_ = client.socket.CloseNow()
	<-client.closed
	<-client.keepaliveDone
}
func (client *Client) IsOnline() bool { return client.owner.Err() == nil }

func (client *Client) start(ctx context.Context, tag string, payload any) (string, *pending, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if !client.IsOnline() {
		return "", nil, errors.New("environment is offline")
	}
	id := strconv.FormatUint(client.sequence.Add(1), 10)
	pending := &pending{events: make(chan envelope, 16)}
	client.mutex.Lock()
	client.requests[id] = pending
	client.mutex.Unlock()
	err := client.send(ctx, map[string]any{"_tag": "Request", "id": id, "tag": tag, "payload": payload, "headers": []any{}})
	if err != nil {
		client.remove(id)
	}
	return id, pending, err
}
func (client *Client) remove(id string) {
	client.mutex.Lock()
	delete(client.requests, id)
	client.mutex.Unlock()
}
func (client *Client) interrupt(id string) {
	ctx, cancel := context.WithTimeout(client.owner, time.Second)
	defer cancel()
	_ = client.send(ctx, map[string]string{"_tag": "Interrupt", "requestId": id})
}

func rpcFailure(cause json.RawMessage) *CallError {
	isAuthorization := false
	var entries []map[string]json.RawMessage
	if json.Unmarshal(cause, &entries) == nil {
		for _, entry := range entries {
			var problem struct {
				Tag string `json:"_tag"`
			}
			if json.Unmarshal(entry["error"], &problem) == nil && problem.Tag == "EnvironmentAuthorizationError" {
				isAuthorization = true
			}
		}
	}
	message := "environment RPC failed"
	if isAuthorization {
		message = "environment grants read-only access"
	}
	return &CallError{Message: message, IsAuthorization: isAuthorization}
}

// Call correlates exactly one Effect terminal response and never replays a request.
func (client *Client) Call(ctx context.Context, tag string, payload any, isWrite bool) (json.RawMessage, error) {
	if isWrite && !client.Authorization.CanOperate() {
		return nil, &CallError{Message: "environment grants read-only access", IsAuthorization: true}
	}
	id, pending, err := client.start(ctx, tag, payload)
	if err != nil {
		return nil, &CallError{Message: "environment request could not be sent", IsOutcomeUnknown: isWrite && id != ""}
	}
	defer client.remove(id)
	select {
	case message := <-pending.events:
		if message.Tag != "Exit" {
			client.interrupt(id)
			return nil, &CallError{Message: "unexpected streaming response", IsOutcomeUnknown: isWrite}
		}
		if message.Exit.Tag == "Success" && len(message.Exit.Value) > 0 {
			return message.Exit.Value, nil
		}
		if message.Exit.Tag == "Failure" {
			return nil, rpcFailure(message.Exit.Cause)
		}
		return nil, &CallError{Message: "invalid environment RPC exit", IsOutcomeUnknown: isWrite}
	case <-ctx.Done():
		client.interrupt(id)
		return nil, &CallError{Message: "environment request was cancelled", IsOutcomeUnknown: isWrite}
	case <-client.closed:
		select {
		case message := <-pending.events:
			if message.Tag == "Exit" && message.Exit.Tag == "Success" && len(message.Exit.Value) > 0 {
				return message.Exit.Value, nil
			}
			if message.Tag == "Exit" && message.Exit.Tag == "Failure" {
				return nil, rpcFailure(message.Exit.Cause)
			}
		default:
		}
		return nil, &CallError{Message: "environment disconnected", IsOutcomeUnknown: isWrite}
	}
}

// Subscribe acknowledges each chunk only after its values have been consumed.
func (client *Client) Subscribe(ctx context.Context, consume func(json.RawMessage) error) error {
	id, pending, err := client.start(ctx, "subscribeServerConfig", map[string]bool{"environmentThemes": true, "usageLimitSources": true})
	if err != nil {
		return err
	}
	defer client.remove(id)
	for {
		select {
		case message := <-pending.events:
			if message.Tag == "Exit" {
				if message.Exit.Tag == "Success" {
					return nil
				}
				return rpcFailure(message.Exit.Cause)
			}
			if message.Tag != "Chunk" {
				return errors.New("invalid environment subscription")
			}
			for _, value := range message.Values {
				if err = consume(value); err != nil {
					client.interrupt(id)
					return err
				}
			}
			if err = client.send(ctx, map[string]string{"_tag": "Ack", "requestId": id}); err != nil {
				return errors.New("environment acknowledgement failed")
			}
		case <-ctx.Done():
			client.interrupt(id)
			return ctx.Err()
		case <-client.closed:
			return errors.New("environment disconnected")
		}
	}
}

func (client *Client) Probe(ctx context.Context) error {
	tag := "server.getConfig"
	if client.Environment.Capabilities["connectionProbe"] {
		tag = "server.probe"
	}
	_, err := client.Call(ctx, tag, map[string]any{}, false)
	return err
}

func (client *Client) requireCapability(name string) error {
	if !client.IsOnline() {
		return errors.New("environment is offline")
	}
	if !client.Environment.Capabilities[name] {
		return fmt.Errorf("update server to support %s", name)
	}
	return nil
}
