package storage

import (
	"context"
	"encoding/json"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	envKeyPrefix = "env:"
)

type Environment struct {
	ID       string   `json:"id"`
	Image    string   `json:"image"`
	Language string   `json:"language"`
	Packages []string `json:"packages"`
}

type EnvironmentStorage struct {
	rdb *redis.Client
}

func NewEnvironmentStorage(rdb *redis.Client) *EnvironmentStorage {
	return &EnvironmentStorage{rdb: rdb}
}

func (s *EnvironmentStorage) Get(ctx context.Context, id string) (*Environment, error) {
	key := envKeyPrefix + id
	data, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, nil
	}

	env := &Environment{
		ID:       data["id"],
		Image:    data["image"],
		Language: data["language"],
	}

	if data["packages"] != "" {
		if err := json.Unmarshal([]byte(data["packages"]), &env.Packages); err != nil {
			return nil, err
		}
	}

	return env, nil
}

func (s *EnvironmentStorage) Set(ctx context.Context, env *Environment) error {
	key := envKeyPrefix + env.ID

	packagesJSON, err := json.Marshal(env.Packages)
	if err != nil {
		return err
	}

	fields := map[string]interface{}{
		"id":       env.ID,
		"image":    env.Image,
		"language": env.Language,
		"packages": string(packagesJSON),
	}

	return s.rdb.HSet(ctx, key, fields).Err()
}

func (s *EnvironmentStorage) Delete(ctx context.Context, id string) error {
	key := envKeyPrefix + id
	return s.rdb.Del(ctx, key).Err()
}

func (s *EnvironmentStorage) List(ctx context.Context, languageFilter string) ([]*Environment, error) {
	pattern := envKeyPrefix + "*"
	keys, err := s.rdb.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	var environments []*Environment
	for _, key := range keys {
		env, err := s.Get(ctx, key[len(envKeyPrefix):])
		if err != nil {
			zap.L().Warn("Failed to get environment", zap.String("key", key), zap.Error(err))
			continue
		}
		if env == nil {
			continue
		}

		if languageFilter == "" || env.Language == languageFilter {
			environments = append(environments, env)
		}
	}

	return environments, nil
}

func (s *EnvironmentStorage) Exists(ctx context.Context, id string) (bool, error) {
	key := envKeyPrefix + id
	count, err := s.rdb.Exists(ctx, key).Result()
	return count > 0, err
}

func (s *EnvironmentStorage) GetClient() *redis.Client {
	return s.rdb
}

