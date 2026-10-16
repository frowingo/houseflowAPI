package appEvents

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

const (
	maxConnections        = 256
	maxConnectionsPerUser = 4
	queueSize             = 16
	writeTimeout          = 5 * time.Second
	pongTimeout           = 60 * time.Second
	pingInterval          = 20 * time.Second
	publishTimeout        = 750 * time.Millisecond
)

type Service struct {
	bus     coordinationAbstract.AppEventBus
	houses  databaseAbstract.DbRepository[entities.House]
	users   databaseAbstract.DbRepository[entities.User]
	origins []string

	mutex   sync.RWMutex
	clients map[string]map[*client]struct{}
	online  bool
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}
}

type Stats struct {
	Online            bool `json:"online"`
	ActiveConnections int  `json:"activeConnections"`
}

func (service *Service) Stats() Stats {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	result := Stats{Online: service.online}
	for _, group := range service.clients {
		result.ActiveConnections += len(group)
	}
	return result
}

func NewService(
	bus coordinationAbstract.AppEventBus,
	houses databaseAbstract.DbRepository[entities.House],
	users databaseAbstract.DbRepository[entities.User],
	origins []string,
) *Service {
	return &Service{bus: bus, houses: houses, users: users, origins: origins, clients: make(map[string]map[*client]struct{})}
}

func (service *Service) Start(ctx context.Context) {
	if service.bus == nil {
		return
	}
	service.mutex.Lock()
	if service.done != nil || service.closed {
		service.mutex.Unlock()
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	service.cancel = cancel
	service.done = make(chan struct{})
	service.mutex.Unlock()
	go service.run(workerCtx)
}

func (service *Service) run(ctx context.Context) {
	defer close(service.done)
	for ctx.Err() == nil {
		subscription, err := service.bus.SubscribeAppEvents(ctx)
		if err != nil {
			log.Printf("app events subscription unavailable: %v", err)
		} else {
			service.mutex.Lock()
			service.online = true
			service.mutex.Unlock()
			service.consume(ctx, subscription)
			_ = subscription.Close()
		}
		service.disconnectAll()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (service *Service) consume(ctx context.Context, subscription coordinationAbstract.AppEventSubscription) {
	health := time.NewTicker(5 * time.Second)
	defer health.Stop()
	for {
		select {
		case event, ok := <-subscription.Events():
			if !ok {
				return
			}
			service.deliver(event)
		case err, ok := <-subscription.Errors():
			if ok && err != nil {
				log.Printf("app events subscription lost: %v", err)
			}
			return
		case <-ctx.Done():
			return
		case <-health.C:
			if !service.bus.Available() {
				return
			}
		}
	}
}

func (service *Service) Close(ctx context.Context) error {
	service.mutex.Lock()
	service.closed = true
	cancel, done := service.cancel, service.done
	service.mutex.Unlock()
	if cancel != nil {
		cancel()
	}
	service.disconnectAll()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) disconnectAll() {
	service.mutex.Lock()
	service.online = false
	clients := make([]*client, 0)
	for _, group := range service.clients {
		for connection := range group {
			clients = append(clients, connection)
		}
	}
	service.mutex.Unlock()
	for _, connection := range clients {
		connection.close()
	}
}

func (service *Service) deliver(event coordinationAbstract.AppEvent) {
	if event.Type != coordinationAbstract.HouseChangedEvent && event.Type != coordinationAbstract.UserChangedEvent {
		return
	}
	message, err := json.Marshal(struct {
		Type    string `json:"type"`
		HouseID string `json:"houseId,omitempty"`
	}{event.Type, event.HouseID})
	if err != nil {
		return
	}
	service.mutex.RLock()
	for _, userID := range event.UserIDs {
		for connection := range service.clients[userID] {
			select {
			case connection.outbound <- message:
			default:
				connection.close()
			}
		}
	}
	service.mutex.RUnlock()
}

func (service *Service) HouseChanged(ctx context.Context, houseID string, refreshHouseList bool) {
	if service.bus == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	id, err := helpers.ToMongoId(houseID)
	if err != nil {
		return
	}
	house, err := service.houses.FindByID(ctx, id)
	if err != nil {
		log.Printf("app events house lookup failed: %v", err)
		return
	}
	service.publish(ctx, coordinationAbstract.AppEvent{
		Type: coordinationAbstract.HouseChangedEvent, HouseID: houseID, UserIDs: house.MemberIds,
	})
	if refreshHouseList {
		service.publish(ctx, coordinationAbstract.AppEvent{
			Type: coordinationAbstract.UserChangedEvent, UserIDs: house.MemberIds,
		})
	}
}

func (service *Service) UsersChanged(ctx context.Context, userIDs ...string) {
	if service.bus == nil || len(userIDs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	service.publish(ctx, coordinationAbstract.AppEvent{Type: coordinationAbstract.UserChangedEvent, UserIDs: userIDs})
}

func (service *Service) publish(ctx context.Context, event coordinationAbstract.AppEvent) {
	if err := service.bus.PublishAppEvent(ctx, event); err != nil {
		log.Printf("app event publish failed: %v", err)
	}
}

func (service *Service) UpgradeMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		if !service.originAllowed(c.Get(fiber.HeaderOrigin)) {
			return c.SendStatus(fiber.StatusForbidden)
		}
		userID, _ := c.Locals("userID").(string)
		service.mutex.RLock()
		available := service.online && !service.closed
		userConnections := len(service.clients[userID])
		count := 0
		for _, group := range service.clients {
			count += len(group)
		}
		service.mutex.RUnlock()
		if !available || count >= maxConnections || userConnections >= maxConnectionsPerUser {
			return c.SendStatus(fiber.StatusServiceUnavailable)
		}
		id, err := helpers.ToMongoId(userID)
		if err != nil {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		lookupCtx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
		defer cancel()
		if _, err := service.users.FindByID(lookupCtx, id); err != nil {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		c.Locals("appEventUserID", strings.Clone(userID))
		return c.Next()
	}
}

func (service *Service) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowed := range service.origins {
		if allowed == "*" || strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

func (service *Service) Handler() fiber.Handler {
	return websocket.New(service.handleConnection, websocket.Config{
		HandshakeTimeout: 5 * time.Second,
		Origins:          []string{"*"},
		ReadBufferSize:   256,
		WriteBufferSize:  256,
	})
}
