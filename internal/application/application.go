package application

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/adminaccount"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/config"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/game"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/platform/database"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/publicgame"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/role"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Application struct {
	Handler http.Handler
	Port    string
	client  *mongo.Client
}

func New(ctx context.Context, logger *slog.Logger) (*Application, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, errors.New("configuration invalid")
	}

	client, err := database.Connect(ctx, cfg.MongoURL)
	if err != nil {
		return nil, errors.New("MongoDB connection failed")
	}
	db := client.Database(cfg.MongoDatabase)
	adminModel := auth.NewMongoAdminAuthRepository(db)
	gameModel := game.NewMongoGameRepository(db)
	publicGameModel := publicgame.NewMongoRepository(db)
	adminAccountModel := adminaccount.NewMongoRepository(db)
	roleModel := role.NewMongoRepository(db)

	indexCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	if err := adminModel.EnsureIndexes(indexCtx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, errors.New("MongoDB index setup failed")
	}
	if err := gameModel.EnsureIndexes(indexCtx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, errors.New("MongoDB game index setup failed")
	}
	if err := publicGameModel.EnsureIndexes(indexCtx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, errors.New("MongoDB public game index setup failed")
	}

	tokenManager := auth.TokenManager{Secret: cfg.JWTSecret, Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience}
	service := &auth.AuthService{Data: adminModel, Limiter: adminModel, Tokens: tokenManager, Passwords: auth.Argon2id{}}
	authHandler := auth.NewAdminAuthHandler(service, adminModel, tokenManager, cfg.Origins, cfg.TrustedProxyCIDRs, logger, cfg.AllowInsecureHTTP)
	mux := http.NewServeMux()
	authHandler.Register(mux)
	game.NewAdminGameHandler(&game.GameService{Data: gameModel}, authHandler, cfg.TrustedProxyCIDRs).Register(mux)
	role.NewAdminRoleHandler(&role.Service{Data: roleModel}, adminModel, authHandler, cfg.TrustedProxyCIDRs).Register(mux)
	adminaccount.NewAdminAccountHandler(&adminaccount.Service{Data: adminAccountModel}, adminModel, authHandler, cfg.TrustedProxyCIDRs).Register(mux)
	publicgame.NewHandler(&publicgame.Service{Data: publicGameModel}, cfg.TrustedProxyCIDRs).Register(mux)

	return &Application{
		Handler: authHandler.CORS(auth.RequestLogging(logger, mux)),
		Port:    cfg.Port,
		client:  client,
	}, nil
}

func (a *Application) Close(ctx context.Context) error {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.Disconnect(ctx)
}
