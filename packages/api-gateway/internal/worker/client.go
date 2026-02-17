package worker

import (
	"context"
	"encoding/json"
	"time"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/redis/go-redis/v9"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/storage"
)

type Client struct {
	rdb *redis.Client
}

func NewClient(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

type ExecutionRequest struct {
	JobID      string            `json:"job_id"`
	Type       storage.JobType   `json:"type"`
	Environment string           `json:"environment"`
	Code       string            `json:"code,omitempty"`
	Files      []storage.JobFile `json:"files,omitempty"`
	Command    string            `json:"command,omitempty"`
	Stdin      string            `json:"stdin"`
	Timeout    int               `json:"timeout"`
	Network    bool              `json:"network"`
}

type ExecutionResponse struct {
	ExitCode        int   `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExecutionTimeMs int64 `json:"execution_time_ms"`
}

func (c *Client) ExecuteSync(ctx context.Context, req *ExecutionRequest) (*ExecutionResponse, error) {
	jobID := req.JobID
	if jobID == "" {
		id, err := gonanoid.New()
		if err != nil {
			return nil, err
		}
		jobID = id
	}

	channel := "worker:execute:" + jobID
	pubsub := c.rdb.Subscribe(ctx, channel)
	defer pubsub.Close()

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	if err := c.rdb.Publish(ctx, "worker:jobs", string(reqJSON)).Err(); err != nil {
		return nil, err
	}

	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()

	ch := pubsub.Channel()
	select {
	case msg := <-ch:
		var resp ExecutionResponse
		if err := json.Unmarshal([]byte(msg.Payload), &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) GetAvailableWorkers(ctx context.Context) (int, error) {
	keys, err := c.rdb.Keys(ctx, "worker:*").Result()
	if err != nil {
		return 0, err
	}

	activeWorkers := 0
	for _, key := range keys {
		ttl, err := c.rdb.TTL(ctx, key).Result()
		if err != nil {
			continue
		}
		if ttl > 0 {
			activeWorkers++
		}
	}

	return activeWorkers, nil
}


