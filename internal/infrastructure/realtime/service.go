package realtime

import (
	"context"
	"errors"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/infrastructure/cqrs"

	"github.com/gofiber/fiber/v2"
)

type Service struct {
	manager *RoomManager
	gateway *Gateway
}

func NewService(
	coordinator coordinationAbstract.Coordinator,
	repository gameAbstract.GameSessionRepository,
	sender cqrs.Sender,
	roomOptions RoomManagerOptions,
	gatewayOptions GatewayOptions,
) (*Service, error) {
	manager, err := NewRoomManager(coordinator, repository, sender, roomOptions)
	if err != nil {
		return nil, err
	}
	gateway, err := NewGateway(coordinator, manager, sender, gatewayOptions)
	if err != nil {
		return nil, err
	}
	return &Service{manager: manager, gateway: gateway}, nil
}

func (service *Service) Start(ctx context.Context) error {
	if err := service.manager.Start(ctx); err != nil {
		return err
	}
	if err := service.gateway.Start(ctx); err != nil {
		closeErr := service.manager.Close(context.Background())
		return errors.Join(err, closeErr)
	}
	return nil
}

func (service *Service) UpgradeMiddleware() fiber.Handler {
	return service.gateway.UpgradeMiddleware()
}

func (service *Service) Handler() fiber.Handler {
	return service.gateway.Handler()
}

func (service *Service) Close(ctx context.Context) error {
	return errors.Join(service.gateway.Close(ctx), service.manager.Close(ctx))
}

func (service *Service) Errors() <-chan RuntimeError {
	return service.gateway.Errors()
}
