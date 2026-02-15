package redis

import (
	"context"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Client struct {
	rdb *redis.Client
}

func NewClient(address, password string, db int) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     address,
		Password: password,
		DB:       db,
	})

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	zap.L().Info("Connected to Redis", zap.String("address", address))

	return &Client{rdb: rdb}, nil
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

func (c *Client) GetClient() *redis.Client {
	return c.rdb
}


