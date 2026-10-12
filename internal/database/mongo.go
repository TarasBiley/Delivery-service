package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func NewMongo(
	ctx context.Context,
	uri string,
) (*mongo.Client, error) {

	client, err := mongo.Connect(
		options.Client().ApplyURI(uri),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"connect mongo: %w",
			err,
		)
	}

	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)

		return nil, fmt.Errorf(
			"ping mongo: %w",
			err,
		)
	}

	return client, nil
}
