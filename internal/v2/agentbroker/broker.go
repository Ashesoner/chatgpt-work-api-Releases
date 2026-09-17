package agentbroker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/v2/agentprotocol"
	"github.com/AAAYNMMM/CWapi/internal/v2/attachments"
	"github.com/AAAYNMMM/CWapi/internal/v2/mcpserver"
)

const (
	DefaultMaxPending         = 16
	DefaultMaxInflight        = 4
	DefaultMaxBatchBytes      = 1024 * 1024
	DefaultActivityTimeout    = 3 * time.Minute
	DefaultMaxRequestLifetime = 30 * time.Minute
	DefaultMaxImageMemory     = 64 * 1024 * 1024
	DefaultWaitTimeout        = 45 * time.Second
	DefaultBridgeLease        = 2 * time.Minute
	DefaultReceiptTTL         = 5 * time.Minute
	DefaultHeartbeat          = 15 * time.Second
)

const (
	StateQueued          = "QUEUED"
	StateClaimed         = "CLAIMED"
	StateRunning         = "RUNNING"
	StateWaitingTool     = "WAITING_TOOL"
	StateCompleted       = "COMPLETED"
	StateFailedRetryable = "FAILED_RETRYABLE"
	StateFailedFinal     = "FAILED_FINAL"
	StateCanceled        = "CANCELED"
	StateTimedOut        = "TIMED_OUT"
	StateDisconnected    = "DISCONNECTED"
	StateFailed          = StateFailedFinal
)

type Config struct {
	MaxPending         int
	MaxInflight        int
	MaxBatchBytes      int
	RequestTimeout     time.Duration // backwards-compatible alias for ActivityTimeout
	ActivityTimeout    time.Duration
	MaxRequestLifetime time.Duration
	MaxImageMemory     int64
	WaitTimeout        time.Duration
	BridgeLease        time.Duration
	ReceiptTTL         time.Duration
	Heartbeat          time.Duration
}

type RequestSnapshot struct {
	RequestID      string `json:"request_id"`
	TaskID         string `json:"task_id,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	State          string `json:"state"`
	Progress       string `json:"progress,omitempty"`
	LastActivity   string `json:"last_activity"`
	HardDeadlineAt string `json:"hard_deadline_at"`
}

type Snapshot struct {
	BridgeState     string            `json:"bridge_state"`
	Pending         int               `json:"pending"`
	Claimed         int               `json:"claimed"`
	Active          int               `json:"active"`
	Completed       uint64            `json:"completed"`
	Revision        uint64            `json:"revision"`
	IdleCount       int               `json:"idle_count"`
	LastState       string            `json:"last_state,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
	LastHeartbeatAt string            `json:"last_heartbeat_at,omitempty"`
	LastProgress    string            `json:"last_progress,omitempty"`
	ImageBytes      int64             `json:"image_bytes"`
	Requests        []RequestSnapshot `json:"requests,omitempty"`
}

type Completion = agentprotocol.Completion

type request struct {
	id              string
	bridgeID        string
	taskID          string
	correlationID   string
	conversation    agentprotocol.Conversation
	payload         map[string]any
	payloadBytes    int
	attachmentBytes int64
	attachments     []attachments.Item
	model           string
	stream          bool
	created         time.Time
	claimed         time.Time
	lastDelivered   time.Time
	lastActivity    time.Time
	deadline        time.Time
	hardDeadline    time.Time
	progress        string
	progressAt      time.Time
	streamCh        chan agentprotocol.StreamChunk
	streamChunks    []agentprotocol.StreamChunk
	streamBytes     int
	streamVersion   uint64
	state           string
	previousState   string
	resumeReason    string
	delivery        int
	result          Completion
	errCode         string
	done            chan struct{}
}

type receipt struct {
	bridgeID    string
	fingerprint string
	expires     time.Time
}

type Broker struct {
	mu sync.Mutex

	cfg            Config
	bridgeID       string
	bridgeDeadline time.Time
	queue          []string
	requests       map[string]*request
	receipts       map[string]receipt
	notify         chan struct{}
	closed         bool
	completed      uint64
	revision       uint64
	lastReported   uint64
	idleCount      int
	lastState      string
	lastError      string
	lastHeartbeat  time.Time
	stopHeartbeat  chan struct{}
}

func New(cfg Config) *Broker {
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = DefaultMaxPending
	}
	if cfg.MaxInflight <= 0 || cfg.MaxInflight > cfg.MaxPending {
		cfg.MaxInflight = DefaultMaxInflight
		if cfg.MaxInflight > cfg.MaxPending {
			cfg.MaxInflight = cfg.MaxPending
		}
	}
	if cfg.MaxBatchBytes <= 0 {
		cfg.MaxBatchBytes = DefaultMaxBatchBytes
	}
	if cfg.ActivityTimeout <= 0 {
		if cfg.RequestTimeout > 0 {
			cfg.ActivityTimeout = cfg.RequestTimeout
		} else {
			cfg.ActivityTimeout = DefaultActivityTimeout
		}
	}
	cfg.RequestTimeout = cfg.ActivityTimeout
	if cfg.MaxRequestLifetime <= 0 {
		cfg.MaxRequestLifetime = DefaultMaxRequestLifetime
	}
	if cfg.MaxImageMemory <= 0 {
		cfg.MaxImageMemory = DefaultMaxImageMemory
	}
	if cfg.WaitTimeout <= 0 {
		cfg.WaitTimeout = DefaultWaitTimeout
	}
	if cfg.BridgeLease <= cfg.WaitTimeout {
		cfg.BridgeLease = DefaultBridgeLease
	}
	if cfg.ReceiptTTL <= 0 {
		cfg.ReceiptTTL = DefaultReceiptTTL
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = DefaultHeartbeat
	}
	broker := &Broker{
		cfg: cfg, requests: make(map[string]*request), receipts: make(map[string]receipt), notify: make(chan struct{}), stopHeartbeat: make(chan struct{}),
	}
	go broker.heartbeatLoop()
	return broker
}
func (b *Broker) Open(_ context.Context, _ mcpserver.AgentOpenInput) (mcpserver.AgentOpenOutput, error) {
	if b == nil {
		return mcpserver.AgentOpenOutput{}, errors.New("AGENT_BROKER_UNAVAILABLE")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return mcpserver.AgentOpenOutput{}, errors.New("AGENT_BROKER_CLOSED")
	}
	now := time.Now()
	b.expireRequestsLocked(now)
	b.expireBridgeLocked(now)
	if b.bridgeID != "" {
		b.touchBridgeLocked(now)
		b.heartbeatLocked(now)
		return mcpserver.AgentOpenOutput{State: "ready", Resumed: true, MaxInflight: b.cfg.MaxInflight, Revision: b.revision}, nil
	}
	resumed := b.activeCountLocked() > 0
	b.bridgeID = "bridge_" + rand.Text()
	for _, req := range b.requests {
		if req == nil || !isActiveRequestState(req.state) {
			continue
		}
		if req.delivery > 0 {
			req.previousState = req.state
			if req.resumeReason == "" {
				req.resumeReason = "bridge_resumed"
			}
		}
		req.bridgeID = b.bridgeID
	}
	b.touchBridgeLocked(now)
	b.heartbeatLocked(now)
	b.lastReported = 0
	b.idleCount = 0
	b.transitionLocked("READY", "")
	b.signalLocked()
	return mcpserver.AgentOpenOutput{State: "ready", Resumed: resumed, MaxInflight: b.cfg.MaxInflight, Revision: b.revision}, nil
}

func (b *Broker) Exchange(ctx context.Context, input mcpserver.AgentExchangeInput) (mcpserver.AgentExchangeOutput, error) {
	if b == nil {
		return mcpserver.AgentExchangeOutput{}, errors.New("AGENT_BROKER_UNAVAILABLE")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	capacity := input.Capacity
	if capacity <= 0 || capacity > b.cfg.MaxInflight {
		capacity = b.cfg.MaxInflight
	}
	started := time.Now()
	preparedResponses := prepareResponses(input.Responses)

	b.mu.Lock()
	now := time.Now()
	b.cleanupReceiptsLocked(now)
	b.expireRequestsLocked(now)
	b.expireBridgeLocked(now)
	if b.closed {
		b.mu.Unlock()
		return mcpserver.AgentExchangeOutput{}, errors.New("AGENT_BROKER_CLOSED")
	}
	if b.bridgeID == "" {
		b.mu.Unlock()
		return mcpserver.AgentExchangeOutput{}, errors.New("AGENT_BRIDGE_NOT_ACTIVE")
	}
	bridgeID := b.bridgeID
	b.mu.Unlock()
	b.validatePreparedResponses(bridgeID, preparedResponses)

	timer := time.NewTimer(b.cfg.WaitTimeout)
	defer timer.Stop()
	var results []mcpserver.AgentExchangeResult
	var events []mcpserver.AgentEvent
	responsesProcessed := false
	followupExpected := false
	activityOnly := len(input.Responses) == 0 && (len(input.Progress) > 0 || len(input.StreamChunks) > 0)

	for {
		b.mu.Lock()
		now = time.Now()
		b.cleanupReceiptsLocked(now)
		b.expireRequestsLocked(now)
		b.expireBridgeLocked(now)
		if err := b.requireBridgeLocked(bridgeID); err != nil {
			b.mu.Unlock()
			return mcpserver.AgentExchangeOutput{}, err
		}
		b.touchBridgeLocked(now)
		if !responsesProcessed {
			var responseEvents []mcpserver.AgentEvent
			results, followupExpected, responseEvents = b.acceptPreparedResponsesLocked(bridgeID, preparedResponses, now)
			events = append(events, responseEvents...)
			events = append(events, b.acceptProgressLocked(bridgeID, input.Progress, now)...)
			events = append(events, b.acceptStreamChunksLocked(bridgeID, input.StreamChunks, now)...)
			responsesProcessed = true
			if activityOnly {
				output := b.exchangeOutputLocked("activity", results, nil, events, started)
				b.mu.Unlock()
				return output, nil
			}
		}
		requests := b.nextBatchLocked(bridgeID, capacity, now)
		if len(requests) > 0 {
			b.touchBridgeLocked(now)
			output := b.exchangeOutputLocked("requests", results, requests, events, started)
			b.mu.Unlock()
			return output, nil
		}
		if len(results) > 0 && !followupExpected {
			output := b.exchangeOutputLocked("responses", results, nil, events, started)
			b.mu.Unlock()
			return output, nil
		}
		notify := b.notify
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return mcpserver.AgentExchangeOutput{}, ctx.Err()
		case <-timer.C:
			b.mu.Lock()
			now = time.Now()
			b.expireRequestsLocked(now)
			b.expireBridgeLocked(now)
			if err := b.requireBridgeLocked(bridgeID); err != nil {
				b.mu.Unlock()
				return mcpserver.AgentExchangeOutput{}, err
			}
			b.touchBridgeLocked(now)
			output := b.exchangeOutputLocked("no_request", results, nil, events, started)
			b.mu.Unlock()
			return output, nil
		case <-notify:
		}
	}
}
func (b *Broker) Close(_ context.Context, _ mcpserver.AgentCloseInput) (mcpserver.AgentCloseOutput, error) {
	if b == nil {
		return mcpserver.AgentCloseOutput{}, errors.New("AGENT_BROKER_UNAVAILABLE")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.expireRequestsLocked(now)
	b.expireBridgeLocked(now)
	if b.closed {
		return mcpserver.AgentCloseOutput{}, errors.New("AGENT_BROKER_CLOSED")
	}
	if b.bridgeID == "" {
		return mcpserver.AgentCloseOutput{State: "no_active_bridge"}, nil
	}
	b.closeBridgeLocked("AGENT_BRIDGE_CLOSED")
	return mcpserver.AgentCloseOutput{State: "closed"}, nil
}
func (b *Broker) Enqueue(conversation agentprotocol.Conversation) (*RequestHandle, error) {
	return b.EnqueueWithAttachments(conversation, attachments.Batch{})
}

func (b *Broker) EnqueueWithAttachments(conversation agentprotocol.Conversation, batch attachments.Batch) (*RequestHandle, error) {
	if b == nil {
		return nil, errors.New("AGENT_BROKER_UNAVAILABLE")
	}
	// Canonical projection, JSON sizing, and media validation are deliberately
	// outside the broker mutex. The lock protects state, not request parsing.
	payloadCopy, err := agentprotocol.EncodeBridgeRequest(conversation)
	if err != nil {
		return nil, err
	}
	payloadJSON, err := json.Marshal(payloadCopy)
	if err != nil {
		return nil, errors.New("AGENT_REQUEST_JSON_INVALID")
	}
	if len(payloadJSON) > b.cfg.MaxBatchBytes {
		return nil, errors.New("AGENT_REQUEST_TOO_LARGE")
	}
	policy := attachments.AgentPolicy()
	if batch.TotalBytes > policy.MaxBatchBytes || len(batch.Items) > policy.MaxFiles {
		return nil, errors.New("ATTACHMENT_BATCH_TOO_LARGE")
	}
	if err := validateImageBindings(conversation, batch); err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errors.New("AGENT_BROKER_CLOSED")
	}
	now := time.Now().UTC()
	b.expireRequestsLocked(now)
	b.expireBridgeLocked(now)
	if b.bridgeID == "" {
		return nil, errors.New("AGENT_BRIDGE_UNAVAILABLE")
	}
	if b.activeCountLocked() >= b.cfg.MaxPending {
		return nil, errors.New("AGENT_BUSY")
	}
	if batch.TotalBytes > 0 && b.imageBytesLocked()+batch.TotalBytes > b.cfg.MaxImageMemory {
		return nil, errors.New("AGENT_MEDIA_BUSY")
	}

	requestID := "request_" + rand.Text()
	taskID, correlationID := requestIdentity(conversation.Metadata)
	hardDeadline := now.Add(b.cfg.MaxRequestLifetime)
	activityDeadline := now.Add(b.cfg.ActivityTimeout)
	if activityDeadline.After(hardDeadline) {
		activityDeadline = hardDeadline
	}
	var streamCh chan agentprotocol.StreamChunk
	if conversation.Stream {
		streamCh = make(chan agentprotocol.StreamChunk, 128)
	}
	req := &request{
		id: requestID, bridgeID: b.bridgeID, taskID: taskID, correlationID: correlationID,
		conversation: conversation, payload: payloadCopy, payloadBytes: len(payloadJSON),
		attachmentBytes: batch.TotalBytes, attachments: append([]attachments.Item(nil), batch.Items...),
		model: strings.TrimSpace(conversation.Model), stream: conversation.Stream, streamCh: streamCh,
		created: now, lastActivity: now, deadline: activityDeadline, hardDeadline: hardDeadline,
		state: StateQueued, done: make(chan struct{}),
	}
	b.requests[req.id] = req
	b.queue = append(b.queue, req.id)
	b.transitionLocked(StateQueued, "")
	b.signalLocked()
	return &RequestHandle{broker: b, id: req.id, done: req.done, deadline: req.deadline}, nil
}

func (b *Broker) Snapshot() Snapshot {
	if b == nil {
		return Snapshot{BridgeState: "OFFLINE"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	b.cleanupReceiptsLocked(now)
	b.expireRequestsLocked(now)
	b.expireBridgeLocked(now)
	pending, claimed := b.requestCountsLocked()
	state := "OFFLINE"
	if b.bridgeID != "" {
		state = "READY"
		if claimed > 0 {
			state = "BUSY"
		}
	}
	lastHeartbeat := ""
	if !b.lastHeartbeat.IsZero() {
		lastHeartbeat = b.lastHeartbeat.UTC().Format(time.RFC3339Nano)
	}
	requests := make([]RequestSnapshot, 0, b.activeCountLocked())
	for _, req := range b.requests {
		if req == nil || !isActiveRequestState(req.state) {
			continue
		}
		requests = append(requests, RequestSnapshot{
			RequestID: req.id, TaskID: req.taskID, CorrelationID: req.correlationID, State: req.state,
			Progress: req.progress, LastActivity: formatTime(req.lastActivity), HardDeadlineAt: formatTime(req.hardDeadline),
		})
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].RequestID < requests[j].RequestID })
	return Snapshot{
		BridgeState: state, Pending: pending, Claimed: claimed, Active: pending + claimed,
		Completed: b.completed, Revision: b.revision, IdleCount: b.idleCount,
		LastState: b.lastState, LastError: b.lastError, LastHeartbeatAt: lastHeartbeat, LastProgress: b.lastProgressLocked(),
		ImageBytes: b.imageBytesLocked(), Requests: requests,
	}
}

func (b *Broker) Shutdown() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.stopHeartbeat)
	for _, req := range b.requests {
		if req != nil && isActiveRequestState(req.state) {
			b.finishLocked(req, StateFailedFinal, "AGENT_BROKER_CLOSED", Completion{})
		}
	}
	b.closeBridgeLocked("AGENT_BROKER_CLOSED")
}

func (b *Broker) nextBatchLocked(bridgeID string, capacity int, now time.Time) []mcpserver.AgentExchangeRequest {
	b.expireRequestsLocked(now)
	inflight := make([]*request, 0, b.cfg.MaxInflight)
	for _, req := range b.requests {
		if req != nil && req.bridgeID == bridgeID && isInflightRequestState(req.state) {
			inflight = append(inflight, req)
		}
	}
	sort.Slice(inflight, func(i, j int) bool {
		if inflight[i].created.Equal(inflight[j].created) {
			return inflight[i].id < inflight[j].id
		}
		return inflight[i].created.Before(inflight[j].created)
	})
	batch := make([]mcpserver.AgentExchangeRequest, 0, capacity)
	batchBytes := 0
	attachmentBytes := int64(0)
	attachmentLimit := attachments.AgentPolicy().MaxBatchBytes
	appendRequest := func(req *request) bool {
		if req == nil || len(batch) >= capacity || batchBytes+req.payloadBytes > b.cfg.MaxBatchBytes || attachmentBytes+req.attachmentBytes > attachmentLimit {
			return false
		}
		if req.claimed.IsZero() {
			req.claimed = now.UTC()
		}
		previousState, resumeReason := req.previousState, req.resumeReason
		if req.delivery > 0 {
			previousState = req.state
			if resumeReason == "" {
				resumeReason = "redelivery"
			}
		}
		req.delivery++
		req.lastDelivered = now.UTC()
		b.refreshActivityLocked(req, now)
		if req.state != StateRunning {
			req.previousState = req.state
			req.state = StateRunning
			b.transitionLocked(StateRunning, "")
		}
		batchBytes += req.payloadBytes
		attachmentBytes += req.attachmentBytes
		metadata := make([]attachments.Metadata, 0, len(req.attachments))
		for _, item := range req.attachments {
			metadata = append(metadata, item.Metadata)
		}
		batch = append(batch, mcpserver.AgentExchangeRequest{
			RequestID: req.id, TaskID: req.taskID, CorrelationID: req.correlationID, State: "claimed", LifecycleState: req.state,
			Delivery: req.delivery, PreviousState: previousState, ResumeReason: resumeReason,
			CreatedAt: req.created.UTC().Format(time.RFC3339Nano), ClaimedAt: req.claimed.UTC().Format(time.RFC3339Nano),
			LastDeliveredAt: req.lastDelivered.UTC().Format(time.RFC3339Nano), LastActivity: req.lastActivity.UTC().Format(time.RFC3339Nano),
			DeadlineAt: formatTime(req.deadline), ActivityDeadlineAt: formatTime(req.deadline), HardDeadlineAt: formatTime(req.hardDeadline),
			Progress: req.progress, ProgressAt: formatTime(req.progressAt), Event: requestEvent(req.conversation), Request: req.payload,
			Attachments: metadata, ContentItems: append([]attachments.Item(nil), req.attachments...),
		})
		return true
	}
	for _, req := range inflight {
		if !appendRequest(req) {
			break
		}
	}
	inflightCount := len(inflight)
	for len(batch) < capacity && inflightCount < b.cfg.MaxInflight && len(b.queue) > 0 {
		requestID := b.queue[0]
		req := b.requests[requestID]
		if req == nil || req.state != StateQueued || (req.bridgeID != "" && req.bridgeID != bridgeID) {
			b.queue = b.queue[1:]
			continue
		}
		if !now.Before(req.deadline) {
			b.queue = b.queue[1:]
			b.finishLocked(req, StateTimedOut, "AGENT_REQUEST_TIMEOUT", Completion{})
			continue
		}
		if batchBytes+req.payloadBytes > b.cfg.MaxBatchBytes || attachmentBytes+req.attachmentBytes > attachmentLimit {
			break
		}
		b.queue = b.queue[1:]
		req.bridgeID = bridgeID
		req.previousState = req.state
		req.state = StateClaimed
		b.transitionLocked(StateClaimed, "")
		inflightCount++
		if !appendRequest(req) {
			break
		}
	}
	return batch
}
func (b *Broker) requireBridgeLocked(bridgeID string) error {
	if b.closed {
		return errors.New("AGENT_BROKER_CLOSED")
	}
	if b.bridgeID == "" {
		return errors.New("AGENT_BRIDGE_NOT_ACTIVE")
	}
	if bridgeID == "" || bridgeID != b.bridgeID {
		return errors.New("AGENT_BRIDGE_STALE_OPERATION")
	}
	return nil
}
func (b *Broker) touchBridgeLocked(now time.Time) {
	if b.bridgeID != "" {
		b.bridgeDeadline = now.Add(b.cfg.BridgeLease)
	}
}

func (b *Broker) heartbeatLoop() {
	if b == nil {
		return
	}
	ticker := time.NewTicker(b.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.mu.Lock()
			if b.closed {
				b.mu.Unlock()
				return
			}
			now := time.Now().UTC()
			if b.bridgeID != "" && b.activeCountLocked() > 0 {
				b.heartbeatLocked(now)
			} else {
				b.expireRequestsLocked(now)
				b.expireBridgeLocked(now)
			}
			b.mu.Unlock()
		case <-b.stopHeartbeat:
			return
		}
	}
}

func (b *Broker) heartbeatLocked(now time.Time) {
	if b.bridgeID == "" {
		return
	}
	now = now.UTC()
	b.lastHeartbeat = now
	b.touchBridgeLocked(now)
}

func (b *Broker) acceptProgressLocked(bridgeID string, progress []mcpserver.AgentProgress, now time.Time) []mcpserver.AgentEvent {
	if len(progress) == 0 {
		return nil
	}
	events := make([]mcpserver.AgentEvent, 0, len(progress))
	for _, item := range progress {
		requestID := strings.TrimSpace(item.RequestID)
		message := strings.TrimSpace(item.Message)
		req := b.requests[requestID]
		if requestID == "" || message == "" || req == nil || req.bridgeID != bridgeID || !isActiveRequestState(req.state) {
			continue
		}
		b.refreshActivityLocked(req, now)
		req.progress = message
		req.progressAt = now.UTC()
		if req.state == StateClaimed {
			req.previousState = req.state
			req.state = StateRunning
			b.transitionLocked(StateRunning, "")
		}
		events = append(events, mcpserver.AgentEvent{Type: "progress", RequestID: requestID, Message: message, At: now.UTC().Format(time.RFC3339Nano)})
	}
	return events
}

func (b *Broker) expireBridgeLocked(now time.Time) {
	if b.bridgeID == "" || b.bridgeDeadline.IsZero() || now.Before(b.bridgeDeadline) {
		return
	}
	if b.claimedCountLocked(b.bridgeID) > 0 {
		return
	}
	b.closeBridgeLocked("AGENT_BRIDGE_STALE")
}

func (b *Broker) expireRequestsLocked(now time.Time) {
	for _, req := range b.requests {
		if req != nil && isActiveRequestState(req.state) && !now.Before(req.deadline) {
			b.finishLocked(req, StateTimedOut, "AGENT_REQUEST_TIMEOUT", Completion{})
		}
	}
}

func (b *Broker) cleanupReceiptsLocked(now time.Time) {
	for requestID, item := range b.receipts {
		if !now.Before(item.expires) {
			delete(b.receipts, requestID)
		}
	}
}

func (b *Broker) closeBridgeLocked(code string) {
	active := b.bridgeID
	b.bridgeID = ""
	b.bridgeDeadline = time.Time{}
	for _, req := range b.requests {
		if req == nil || !isActiveRequestState(req.state) || req.bridgeID != active {
			continue
		}
		req.previousState = req.state
		req.resumeReason = code
		req.bridgeID = ""
	}
	b.transitionLocked("OFFLINE", code)
	b.signalLocked()
}

func (b *Broker) activeCountLocked() int {
	count := 0
	for _, req := range b.requests {
		if req != nil && isActiveRequestState(req.state) {
			count++
		}
	}
	return count
}

func (b *Broker) claimedCountLocked(bridgeID string) int {
	count := 0
	for _, req := range b.requests {
		if req != nil && req.bridgeID == bridgeID && isInflightRequestState(req.state) {
			count++
		}
	}
	return count
}

func (b *Broker) markRetryableLocked(req *request, code string, now time.Time) {
	if req == nil || !isActiveRequestState(req.state) {
		return
	}
	req.previousState = req.state
	req.state = StateFailedRetryable
	req.errCode = code
	b.refreshActivityLocked(req, now)
	req.resumeReason = "retry_after_error"
	b.transitionLocked(StateFailedRetryable, code)
	b.signalLocked()
}

func (b *Broker) finishLocked(req *request, state, code string, completion Completion) {
	if req == nil || isTerminalRequestState(req.state) {
		return
	}
	req.previousState = req.state
	req.state, req.errCode, req.result = state, code, completion
	if req.streamCh != nil {
		close(req.streamCh)
	}
	req.streamChunks = nil
	req.streamBytes = 0
	b.releaseAttachmentsLocked(req)
	req.lastActivity = time.Now().UTC()
	b.transitionLocked(state, code)
	close(req.done)
	b.signalLocked()
}

func (b *Broker) releaseAttachmentsLocked(req *request) {
	if req == nil {
		return
	}
	req.attachments = nil
	req.attachmentBytes = 0
}

func (b *Broker) refreshActivityLocked(req *request, now time.Time) {
	if req == nil {
		return
	}
	now = now.UTC()
	req.lastActivity = now
	next := now.Add(b.cfg.ActivityTimeout)
	if !req.hardDeadline.IsZero() && next.After(req.hardDeadline) {
		next = req.hardDeadline
	}
	req.deadline = next
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (b *Broker) transitionLocked(state, code string) {
	b.revision++
	b.lastState = state
	b.lastError = code
}

func (b *Broker) exchangeOutputLocked(state string, results []mcpserver.AgentExchangeResult, requests []mcpserver.AgentExchangeRequest, events []mcpserver.AgentEvent, started time.Time) mcpserver.AgentExchangeOutput {
	pending, inflight := b.requestCountsLocked()
	changed := b.revision != b.lastReported
	nextAction := "process_requests"
	switch state {
	case "no_request":
		if changed {
			b.idleCount = 0
			nextAction = "advance_or_finish"
		} else {
			b.idleCount++
			nextAction = "reassess_before_waiting"
		}
	case "responses":
		b.idleCount = 0
		nextAction = "advance_or_finish"
	case "activity":
		b.idleCount = 0
		nextAction = "continue_request"
	default:
		b.idleCount = 0
	}
	b.lastReported = b.revision
	waited := time.Since(started).Milliseconds()
	if waited < 0 {
		waited = 0
	}
	lastHeartbeat := formatTime(b.lastHeartbeat)
	return mcpserver.AgentExchangeOutput{
		State: state,
		Activity: mcpserver.AgentExchangeActivity{
			Revision: b.revision, Changed: changed, Pending: pending, Inflight: inflight,
			Active: pending + inflight, QueuedRequests: pending, ActiveRequests: pending + inflight,
			IdleCount: b.idleCount, WaitedMillis: waited, LastState: b.lastState, LastError: b.lastError,
			LastHeartbeatAt: lastHeartbeat, LastProgress: b.lastProgressLocked(), NextAction: nextAction,
			ImageBytes: b.imageBytesLocked(), Requests: b.requestActivitiesLocked(),
		},
		Results: results, Requests: requests, Events: events,
	}
}

func (b *Broker) requestActivitiesLocked() []mcpserver.AgentRequestActivity {
	result := make([]mcpserver.AgentRequestActivity, 0, b.activeCountLocked())
	for _, req := range b.requests {
		if req == nil || !isActiveRequestState(req.state) {
			continue
		}
		result = append(result, mcpserver.AgentRequestActivity{
			RequestID: req.id, TaskID: req.taskID, CorrelationID: req.correlationID,
			State: req.state, Progress: req.progress, LastActivity: formatTime(req.lastActivity), HardDeadlineAt: formatTime(req.hardDeadline),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RequestID < result[j].RequestID })
	return result
}

func (b *Broker) requestCountsLocked() (pending, inflight int) {
	for _, req := range b.requests {
		if req == nil {
			continue
		}
		if req.state == StateQueued {
			pending++
		} else if isInflightRequestState(req.state) {
			inflight++
		}
	}
	return pending, inflight
}

func (b *Broker) imageBytesLocked() int64 {
	var total int64
	for _, req := range b.requests {
		if req != nil && isActiveRequestState(req.state) {
			total += req.attachmentBytes
		}
	}
	return total
}

func (b *Broker) lastProgressLocked() string {
	var latest time.Time
	message := ""
	for _, req := range b.requests {
		if req == nil || !isActiveRequestState(req.state) || req.progress == "" || req.progressAt.IsZero() {
			continue
		}
		if latest.IsZero() || req.progressAt.After(latest) {
			latest = req.progressAt
			message = req.progress
		}
	}
	return message
}

func isActiveRequestState(state string) bool {
	switch state {
	case StateQueued, StateClaimed, StateRunning, StateFailedRetryable:
		return true
	default:
		return false
	}
}

func isInflightRequestState(state string) bool {
	switch state {
	case StateClaimed, StateRunning, StateFailedRetryable:
		return true
	default:
		return false
	}
}

func isTerminalRequestState(state string) bool {
	switch state {
	case StateWaitingTool, StateCompleted, StateFailedFinal, StateCanceled, StateTimedOut, StateDisconnected:
		return true
	default:
		return false
	}
}

func requestEvent(conversation agentprotocol.Conversation) string {
	for index := len(conversation.Messages) - 1; index >= 0; index-- {
		if conversation.Messages[index].Role == agentprotocol.RoleTool {
			return "tool_result"
		}
		if conversation.Messages[index].Role == agentprotocol.RoleUser || conversation.Messages[index].Role == agentprotocol.RoleAssistant {
			break
		}
	}
	return "request"
}

func responseErrorDetail(err error, requestID string, response map[string]any, retryable bool) *mcpserver.AgentStructuredError {
	code := errorCode(err)
	if code == "" {
		code = "AGENT_RESPONSE_INVALID"
	}
	message := code
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	callID, toolName := responseToolIdentity(response)
	return &mcpserver.AgentStructuredError{
		Code: code, Message: message, RequestID: requestID, ToolCallID: callID, ToolName: toolName, Retryable: retryable,
	}
}

func responseToolIdentity(response map[string]any) (string, string) {
	if response == nil {
		return "", ""
	}
	message := response
	if nested, ok := response["message"].(map[string]any); ok {
		message = nested
	}
	calls, _ := message["tool_calls"].([]any)
	if len(calls) == 0 {
		return "", ""
	}
	call, _ := calls[0].(map[string]any)
	function, _ := call["function"].(map[string]any)
	return strings.TrimSpace(textValue(call["id"])), strings.TrimSpace(textValue(function["name"]))
}

func errorEvent(detail *mcpserver.AgentStructuredError, now time.Time) mcpserver.AgentEvent {
	if detail == nil {
		return mcpserver.AgentEvent{Type: "error", At: now.UTC().Format(time.RFC3339Nano)}
	}
	return mcpserver.AgentEvent{
		Type: "error", RequestID: detail.RequestID, ToolCallID: detail.ToolCallID, ToolName: detail.ToolName,
		Code: detail.Code, Message: detail.Message, Retryable: detail.Retryable, At: now.UTC().Format(time.RFC3339Nano),
	}
}
func (b *Broker) compactQueueLocked(removeID string) {
	if len(b.queue) == 0 {
		return
	}
	filtered := b.queue[:0]
	for _, id := range b.queue {
		if id != removeID {
			filtered = append(filtered, id)
		}
	}
	b.queue = filtered
}

func (b *Broker) signalLocked() {
	close(b.notify)
	b.notify = make(chan struct{})
}

func completionFingerprint(value Completion) (string, bool) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), true
}

func requestIdentity(metadata map[string]any) (string, string) {
	if len(metadata) == 0 {
		return "", ""
	}
	return strings.TrimSpace(textValue(metadata["task_id"])), strings.TrimSpace(textValue(metadata["correlation_id"]))
}

func textValue(value any) string {
	text, _ := value.(string)
	return text
}
