package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ConnectMongo creates a MongoDB client and returns the configured database.
// Keep the client alive for the lifetime of the server so its connection pool
// can be reused across requests.
func ConnectMongo() (*mongo.Client, *mongo.Database, error) {
	uri := strings.TrimSpace(os.Getenv("MONGODB_URL"))
	if uri == "" {
		uri = strings.TrimSpace(os.Getenv("MONGODB_URI"))
	}
	if uri == "" {
		return nil, nil, fmt.Errorf("MONGODB_URL or MONGODB_URI is not set")
	}

	databaseName := os.Getenv("MONGODB_DATABASE")
	if databaseName == "" {
		parsedURI, err := url.Parse(uri)
		if err != nil {
			return nil, nil, fmt.Errorf("parse MongoDB URI: %w", err)
		}
		databaseName = strings.Trim(parsedURI.Path, "/")
	}
	if databaseName == "" {
		return nil, nil, fmt.Errorf("MONGODB_DATABASE is required when MongoDB URI has no database path")
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, nil, fmt.Errorf("create MongoDB client: %w", err)
	}
	return client, client.Database(databaseName), nil
}
