package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/parabeacon/server/internal/flight"
	"github.com/parabeacon/server/internal/model"
	"github.com/redis/go-redis/v9"
)

const (
	broadcastInterval     = 500 * time.Millisecond
	locationMaxAge        = 15 * time.Second
	writeWait             = 5 * time.Second
	pongWait              = 45 * time.Second
	pingPeriod            = 20 * time.Second
	maxMessageBytes       = 4096
	maxFlightStreamPoints = 300_000
)

type Location struct {
	UserID        uuid.UUID `json:"-"`
	ParticipantID string    `json:"participant_id"`
	PilotName     string    `json:"pilot_name"`
	Latitude      float64   `json:"lat"`
	Longitude     float64   `json:"lon"`
	Altitude      float64   `json:"altitude"`
	Speed         float64   `json:"speed"`
	Heading       *float64  `json:"heading,omitempty"`
	GPSAlt        *float64  `json:"gps_altitude,omitempty"`
	PressureAlt   *float64  `json:"pressure_altitude,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

type incomingMessage struct {
	Type      string    `json:"type"`
	Location  Location  `json:"location"`
	Timestamp time.Time `json:"timestamp"`
}

type flightResetAck struct {
	Type    string    `json:"type"`
	ResetAt time.Time `json:"reset_at"`
}
type Snapshot struct {
	Type   string     `json:"type"`
	RoomID uuid.UUID  `json:"room_id"`
	At     time.Time  `json:"tick_at"`
	Users  []Location `json:"users"`
}

type room struct {
	clients   map[*client]struct{}
	locations map[uuid.UUID]Location
}

type client struct {
	hub        *Hub
	conn       *websocket.Conn
	roomID     uuid.UUID
	userID     uuid.UUID
	pilotName  string
	canPublish bool
	send       chan []byte
	done       chan struct{}
	closeOnce  sync.Once
	lastRead   time.Time
}

type Hub struct {
	mu            sync.Mutex
	rooms         map[uuid.UUID]*room
	disabledUsers map[uuid.UUID]bool
	redis         *redis.Client
	lifecycle     *flight.LifecycleEngine
	pointStore    TrackingPointStore
	logger        *log.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	started       bool
	workers       sync.WaitGroup
}

type TrackingPointStore interface {
	StorePoints(context.Context, uuid.UUID, uuid.UUID, []model.LiveTrackPoint) (int64, error)
}

func NewHub(redisClient *redis.Client, lifecycle *flight.LifecycleEngine, logger *log.Logger, pointStores ...TrackingPointStore) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	hub := &Hub{rooms: make(map[uuid.UUID]*room), disabledUsers: make(map[uuid.UUID]bool), redis: redisClient, lifecycle: lifecycle, logger: logger, ctx: ctx, cancel: cancel}
	if len(pointStores) > 0 {
		hub.pointStore = pointStores[0]
	}
	return hub
}

func (h *Hub) Serve(userID, roomID uuid.UUID, pilotName string, canPublish bool, conn *websocket.Conn) {
	if userID == uuid.Nil || roomID == uuid.Nil || conn == nil {
		return
	}
	c := &client{
		hub: h, conn: conn, userID: userID, roomID: roomID, pilotName: pilotName,
		canPublish: canPublish, send: make(chan []byte, 16), done: make(chan struct{}),
	}
	if !h.register(c) {
		_ = conn.Close()
		return
	}
	h.mu.Lock()
	if !h.started && h.ctx.Err() == nil {
		h.started = true
		h.workers.Add(1)
		go h.broadcastLoop()
	}
	h.mu.Unlock()
	go c.writePump()
	c.readPump()
	h.unregister(c)
}

func (h *Hub) register(c *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctx.Err() != nil || h.disabledUsers[c.userID] {
		return false
	}
	r := h.rooms[c.roomID]
	if r == nil {
		r = &room{clients: make(map[*client]struct{}), locations: make(map[uuid.UUID]Location)}
		h.rooms[c.roomID] = r
	}
	r.clients[c] = struct{}{}
	return true
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	r := h.rooms[c.roomID]
	if r != nil {
		delete(r.clients, c)
		stillConnected := false
		for other := range r.clients {
			if other.userID == c.userID {
				stillConnected = true
				break
			}
		}
		if !stillConnected {
			delete(r.locations, c.userID)
		}
		if len(r.clients) == 0 {
			delete(h.rooms, c.roomID)
		}
	}
	c.close()
	h.mu.Unlock()
	if h.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := h.redis.HDel(ctx, redisRoomKey(c.roomID), c.userID.String()).Err(); err != nil && !errors.Is(err, context.Canceled) {
			h.logger.Printf("realtime redis cleanup room=%s user=%s: %v", c.roomID, c.userID, err)
		}
	}
}

func (h *Hub) broadcastLoop() {
	defer h.workers.Done()
	ticker := time.NewTicker(broadcastInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case now := <-ticker.C:
			h.broadcast(now.UTC())
		}
	}
}

func (h *Hub) broadcast(now time.Time) {
	type roomView struct {
		roomID  uuid.UUID
		clients []*client
		local   map[uuid.UUID]Location
		users   []Location
	}
	type pending struct {
		roomID  uuid.UUID
		clients []*client
		payload []byte
	}
	views := make([]roomView, 0)
	var expired []struct{ roomID, userID uuid.UUID }
	h.mu.Lock()
	for roomID, r := range h.rooms {
		if len(r.clients) == 0 {
			continue
		}
		view := roomView{roomID: roomID, local: make(map[uuid.UUID]Location, len(r.locations))}
		for userID, location := range r.locations {
			if now.Sub(location.Timestamp) > locationMaxAge {
				delete(r.locations, userID)
				expired = append(expired, struct{ roomID, userID uuid.UUID }{roomID, userID})
				continue
			}
			view.local[userID] = location
		}
		for c := range r.clients {
			view.clients = append(view.clients, c)
		}
		views = append(views, view)
	}
	h.mu.Unlock()

	stateCommands := make(map[uuid.UUID]*redis.MapStringStringCmd, len(views))
	if h.redis != nil && len(views) > 0 {
		ctx, cancel := context.WithTimeout(h.ctx, 400*time.Millisecond)
		pipe := h.redis.Pipeline()
		for _, view := range views {
			stateCommands[view.roomID] = pipe.HGetAll(ctx, redisRoomKey(view.roomID))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			h.logger.Printf("realtime redis tick read: %v", err)
		}
		cancel()
	}

	var expiredInRedis []struct{ roomID, userID uuid.UUID }
	for index := range views {
		view := &views[index]
		userLocations := make(map[uuid.UUID]Location, len(view.local))
		for userID, location := range view.local {
			location.ParticipantID = uuid.NewSHA1(view.roomID, []byte(userID.String())).String()
			userLocations[userID] = location
		}
		if command := stateCommands[view.roomID]; command != nil {
			values, err := command.Result()
			if err != nil {
				h.logger.Printf("realtime redis state room=%s: %v", view.roomID, err)
			} else {
				for rawUserID, rawLocation := range values {
					userID, parseErr := uuid.Parse(rawUserID)
					var location Location
					if parseErr != nil || json.Unmarshal([]byte(rawLocation), &location) != nil || !validLocation(location) || now.Sub(location.Timestamp) > locationMaxAge {
						if parseErr == nil {
							expiredInRedis = append(expiredInRedis, struct{ roomID, userID uuid.UUID }{view.roomID, userID})
						}
						continue
					}
					if local, exists := userLocations[userID]; exists && local.Timestamp.After(location.Timestamp) {
						continue
					}
					location.UserID = userID
					location.ParticipantID = uuid.NewSHA1(view.roomID, []byte(userID.String())).String()
					userLocations[userID] = location
				}
			}
		}
		view.users = make([]Location, 0, len(userLocations))
		for _, location := range userLocations {
			view.users = append(view.users, location)
		}
		sort.Slice(view.users, func(i, j int) bool { return view.users[i].UserID.String() < view.users[j].UserID.String() })
	}
	for _, item := range expired {
		h.deleteRedisLocation(item.roomID, item.userID)
	}
	if h.redis != nil && len(expiredInRedis) > 0 {
		ctx, cancel := context.WithTimeout(h.ctx, time.Second)
		pipe := h.redis.Pipeline()
		for _, item := range expiredInRedis {
			pipe.HDel(ctx, redisRoomKey(item.roomID), item.userID.String())
		}
		if _, err := pipe.Exec(ctx); err != nil {
			h.logger.Printf("realtime redis stale location cleanup: %v", err)
		}
		cancel()
	}

	for _, view := range views {
		payload, err := json.Marshal(Snapshot{Type: "snapshot", RoomID: view.roomID, At: now, Users: view.users})
		if err != nil {
			h.logger.Printf("encode realtime tick room=%s: %v", view.roomID, err)
			continue
		}
		for _, c := range view.clients {
			select {
			case c.send <- payload:
			case <-c.done:
			default:
				h.logger.Printf("realtime slow client room=%s user=%s; closing connection", view.roomID, c.userID)
				_ = c.conn.Close()
			}
		}
	}
}

func (h *Hub) resetFlight(c *client, resetAt time.Time) error {
	if !c.canPublish {
		return errors.New("room subscription is read-only")
	}
	if h.lifecycle == nil {
		return errors.New("flight lifecycle is unavailable")
	}
	resetAt = resetAt.UTC()
	now := time.Now().UTC()
	if resetAt.IsZero() || now.Sub(resetAt) > 2*time.Minute || resetAt.After(now.Add(5*time.Second)) {
		return errors.New("flight reset timestamp outside accepted range")
	}
	if err := h.lifecycle.Reset(c.userID, resetAt); err != nil {
		return err
	}
	payload, err := json.Marshal(flightResetAck{Type: "flight_reset_ack", ResetAt: resetAt})
	if err != nil {
		return err
	}
	select {
	case c.send <- payload:
	case <-c.done:
	default:
		h.logger.Printf("realtime flight reset ack dropped room=%s user=%s", c.roomID, c.userID)
	}
	return nil
}
func (h *Hub) update(c *client, location Location) error {
	if !c.canPublish {
		return errors.New("room subscription is read-only")
	}
	if !validLocation(location) {
		return errors.New("invalid location payload")
	}
	location.UserID = c.userID
	location.PilotName = c.pilotName
	location.Timestamp = location.Timestamp.UTC()
	if delta := time.Since(location.Timestamp); delta > 2*time.Minute || location.Timestamp.After(time.Now().UTC().Add(5*time.Second)) {
		return errors.New("location timestamp outside accepted range")
	}
	h.mu.Lock()
	if h.disabledUsers[c.userID] {
		h.mu.Unlock()
		return errors.New("live tracking is disabled")
	}
	r := h.rooms[c.roomID]
	if r == nil {
		h.mu.Unlock()
		return errors.New("room is no longer active")
	}
	if previous, exists := r.locations[c.userID]; exists && !location.Timestamp.After(previous.Timestamp) {
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	if h.pointStore != nil {
		point := model.LiveTrackPoint{
			SessionID: c.roomID, UserID: c.userID, Latitude: location.Latitude,
			Longitude: location.Longitude, Altitude: location.Altitude, GroundSpeed: location.Speed,
			GPSAltitude: location.GPSAlt, BarometricAltitude: location.PressureAlt,
			Heading: location.Heading, Timestamp: location.Timestamp,
		}
		if _, err := h.pointStore.StorePoints(h.ctx, c.userID, c.roomID, []model.LiveTrackPoint{point}); err != nil {
			h.logger.Printf("persist realtime location room=%s user=%s: %v", c.roomID, c.userID, err)
			return err
		}
	}
	h.mu.Lock()
	if h.disabledUsers[c.userID] {
		h.mu.Unlock()
		return errors.New("live tracking is disabled")
	}
	r = h.rooms[c.roomID]
	if r == nil {
		h.mu.Unlock()
		return errors.New("room is no longer active")
	}
	if previous, exists := r.locations[c.userID]; exists && !location.Timestamp.After(previous.Timestamp) {
		h.mu.Unlock()
		return nil
	}
	r.locations[c.userID] = location
	h.mu.Unlock()
	if h.redis != nil {
		data, err := json.Marshal(location)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
		defer cancel()
		key := redisRoomKey(c.roomID)
		if err := h.redis.HSet(ctx, key, c.userID.String(), data).Err(); err != nil {
			h.logger.Printf("realtime redis update room=%s user=%s: %v", c.roomID, c.userID, err)
		}
		if err := h.redis.Expire(ctx, key, 30*time.Second).Err(); err != nil {
			h.logger.Printf("realtime redis expiry room=%s: %v", c.roomID, err)
		}
		if err := h.redis.XAdd(ctx, &redis.XAddArgs{
			Stream: redisRoomStreamKey(c.roomID), MaxLen: maxFlightStreamPoints, Approx: true,
			Values: map[string]interface{}{"user_id": c.userID.String(), "point": data},
		}).Err(); err != nil {
			h.logger.Printf("realtime redis stream room=%s user=%s: %v", c.roomID, c.userID, err)
		} else if err := h.redis.Expire(ctx, redisRoomStreamKey(c.roomID), 30*24*time.Hour).Err(); err != nil {
			h.logger.Printf("realtime redis stream expiry room=%s: %v", c.roomID, err)
		}
	}
	if h.lifecycle != nil {
		h.mu.Lock()
		if h.disabledUsers[c.userID] {
			h.mu.Unlock()
			h.deleteRedisRoomData(c.roomID)
			return errors.New("live tracking is disabled")
		}
		if err := h.lifecycle.Observe(c.userID, flight.TrackPoint{
			Latitude: location.Latitude, Longitude: location.Longitude, AltitudeMeters: location.Altitude,
			GPSAltitudeMeters: location.GPSAlt, PressureAltitude: location.PressureAlt,
			GroundSpeedMPS: location.Speed, HeadingDegrees: location.Heading, Timestamp: location.Timestamp,
		}); err != nil {
			h.logger.Printf("flight state update user=%s: %v", c.userID, err)
		}
		h.mu.Unlock()
	}
	return nil
}

func (h *Hub) deleteRedisLocation(roomID, userID uuid.UUID) {
	if h.redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.redis.HDel(ctx, redisRoomKey(roomID), userID.String()).Err(); err != nil {
		h.logger.Printf("realtime redis expiry room=%s user=%s: %v", roomID, userID, err)
	}
}

func (h *Hub) Close() {
	h.cancel()
	h.mu.Lock()
	for _, r := range h.rooms {
		for c := range r.clients {
			c.close()
			_ = c.conn.Close()
		}
	}
	h.rooms = make(map[uuid.UUID]*room)
	h.mu.Unlock()
	h.workers.Wait()
}

func redisRoomKey(roomID uuid.UUID) string { return "parabeacon:realtime:room:" + roomID.String() }

func redisRoomStreamKey(roomID uuid.UUID) string {
	return "parabeacon:realtime:flight:" + roomID.String() + ":points"
}

func validLocation(location Location) bool {
	finite := func(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
	return finite(location.Latitude) && location.Latitude >= -90 && location.Latitude <= 90 &&
		finite(location.Longitude) && location.Longitude >= -180 && location.Longitude <= 180 &&
		finite(location.Altitude) && location.Altitude >= -2000 && location.Altitude <= 30000 &&
		finite(location.Speed) && location.Speed >= 0 && location.Speed <= 200 &&
		(location.Heading == nil || (finite(*location.Heading) && *location.Heading >= 0 && *location.Heading < 360)) &&
		(location.GPSAlt == nil || (finite(*location.GPSAlt) && *location.GPSAlt >= -2000 && *location.GPSAlt <= 30000)) &&
		(location.PressureAlt == nil || (finite(*location.PressureAlt) && *location.PressureAlt >= -2000 && *location.PressureAlt <= 30000)) &&
		!location.Timestamp.IsZero()
}

func (c *client) close() { c.closeOnce.Do(func() { close(c.done) }) }

func (c *client) readPump() {
	defer c.conn.Close()
	c.conn.SetReadLimit(maxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(pongWait)) })
	for {
		var message incomingMessage
		if err := c.conn.ReadJSON(&message); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.hub.logger.Printf("realtime read room=%s user=%s: %v", c.roomID, c.userID, err)
			}
			return
		}
		switch message.Type {
		case "location":
			now := time.Now()
			if !c.lastRead.IsZero() && now.Sub(c.lastRead) < 100*time.Millisecond {
				continue
			}
			c.lastRead = now
			if err := c.hub.update(c, message.Location); err != nil {
				c.hub.logger.Printf("realtime rejected location room=%s user=%s: %v", c.roomID, c.userID, err)
			}
		case "flight_reset":
			if err := c.hub.resetFlight(c, message.Timestamp); err != nil {
				c.hub.logger.Printf("realtime rejected flight reset room=%s user=%s: %v", c.roomID, c.userID, err)
			}
		default:
			c.hub.logger.Printf("realtime invalid message type room=%s user=%s", c.roomID, c.userID)
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() { ticker.Stop(); _ = c.conn.Close() }()
	for {
		select {
		case <-c.done:
			return
		case payload := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func NewUpgrader(allowedOrigins []string) websocket.Upgrader {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = struct{}{}
	}
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		CheckOrigin: func(request *http.Request) bool {
			origin := request.Header.Get("Origin")
			if origin == "" {
				return true
			}
			_, allowed := origins[origin]
			return allowed
		},
	}
}

type RoomAuthorizer interface {
	AuthorizeLiveRoom(context.Context, uuid.UUID, uuid.UUID) (bool, string, bool, error)
}

func NewWebSocketHandler(hub *Hub, allowedOrigins []string, authorizer RoomAuthorizer) gin.HandlerFunc {
	upgrader := NewUpgrader(allowedOrigins)
	return func(c *gin.Context) {
		userValue, exists := c.Get("authenticated_user_id")
		userID, ok := userValue.(uuid.UUID)
		if !exists || !ok || userID == uuid.Nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		roomID, err := uuid.Parse(c.Query("room_id"))
		if err != nil || roomID == uuid.Nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid room_id"})
			return
		}
		if authorizer == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "room authorization is unavailable"})
			return
		}
		canPublish, pilotName, allowed, err := authorizer.AuthorizeLiveRoom(
			c.Request.Context(), userID, roomID,
		)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "room authorization failed"})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "room membership is not authorized"})
			return
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		hub.Serve(userID, roomID, pilotName, canPublish, conn)
	}
}
