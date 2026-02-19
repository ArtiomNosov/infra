package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	queueCode  = "jobs:code:queue"
	queueFiles = "jobs:files:queue"
	keyPrefix  = "job:"
	keyCode    = "job:code:"
	keyFiles   = "job:files:"
	resultTTL  = 4 * time.Hour
)

type JobKind string

const (
	JobKindCode  JobKind = "code"
	JobKindFiles JobKind = "files"
)

type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

type Job struct {
	ID        string     `json:"id"`
	Kind      JobKind    `json:"kind"`
	Status    JobStatus  `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Payload   string     `json:"payload"`   // JSON
	Result    *JobResult `json:"result,omitempty"`
}

type JobResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Error  string `json:"error,omitempty"`
}

type JobPayloadCode struct {
	Lang    string `json:"lang"`
	Code    string `json:"code"`
	Timeout int    `json:"timeout"`
}

type JobPayloadFiles struct {
	Lang    string          `json:"lang"`
	Files   []FilePayload   `json:"files"`
	Command string          `json:"command"`
	Timeout int             `json:"timeout"`
}

type FilePayload struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type Store struct {
	rdb  *redis.Client
	log  *zap.Logger
	ttl  time.Duration
}

func NewStore(rdb *redis.Client, log *zap.Logger, resultTTL time.Duration) *Store {
	if resultTTL <= 0 {
		resultTTL = 4 * time.Hour
	}
	return &Store{rdb: rdb, log: log, ttl: resultTTL}
}

func (s *Store) EnqueueCode(ctx context.Context, payload *JobPayloadCode) (string, error) {
	id := uuid.New().String()
	job := Job{
		ID:        id,
		Kind:      JobKindCode,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	job.Payload = string(payloadBytes)
	key := keyCode + id
	data, err := json.Marshal(job)
	if err != nil {
		return "", err
	}
	if err := s.rdb.Set(ctx, key, data, s.ttl).Err(); err != nil {
		return "", err
	}
	if err := s.rdb.RPush(ctx, queueCode, id).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) EnqueueFiles(ctx context.Context, payload *JobPayloadFiles) (string, error) {
	id := uuid.New().String()
	job := Job{
		ID:        id,
		Kind:      JobKindFiles,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	job.Payload = string(payloadBytes)
	key := keyFiles + id
	data, err := json.Marshal(job)
	if err != nil {
		return "", err
	}
	if err := s.rdb.Set(ctx, key, data, s.ttl).Err(); err != nil {
		return "", err
	}
	if err := s.rdb.RPush(ctx, queueFiles, id).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) DequeueCode(ctx context.Context, timeout time.Duration) (string, error) {
	v, err := s.rdb.BLPop(ctx, timeout, queueCode).Result()
	if err == redis.Nil || len(v) < 2 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v[1], nil
}

func (s *Store) DequeueFiles(ctx context.Context, timeout time.Duration) (string, error) {
	v, err := s.rdb.BLPop(ctx, timeout, queueFiles).Result()
	if err == redis.Nil || len(v) < 2 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v[1], nil
}

func (s *Store) GetJobByID(ctx context.Context, kind JobKind, id string) (*Job, error) {
	key := s.jobKey(kind, id)
	data, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) jobKey(kind JobKind, id string) string {
	if kind == JobKindFiles {
		return keyFiles + id
	}
	return keyCode + id
}

func (s *Store) SetStatus(ctx context.Context, kind JobKind, id string, status JobStatus, result *JobResult) error {
	key := s.jobKey(kind, id)
	data, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return fmt.Errorf("job not found")
	}
	if err != nil {
		return err
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	job.Status = status
	job.UpdatedAt = time.Now().UTC()
	if result != nil {
		job.Result = result
	}
	out, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, out, s.ttl).Err()
}

func (s *Store) CancelJob(ctx context.Context, kind JobKind, id string) error {
	job, err := s.GetJobByID(ctx, kind, id)
	if err != nil || job == nil {
		return err
	}
	if job.Status != StatusPending && job.Status != StatusRunning {
		return nil
	}
	return s.SetStatus(ctx, kind, id, StatusCancelled, &JobResult{Error: "cancelled"})
}

func (s *Store) ListJobs(ctx context.Context, kind JobKind) ([]Job, error) {
	var prefix string
	if kind == JobKindCode {
		prefix = keyCode
	} else {
		prefix = keyFiles
	}
	keys, err := s.rdb.Keys(ctx, prefix+"*").Result()
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for _, key := range keys {
		data, err := s.rdb.Get(ctx, key).Bytes()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			continue
		}
		var job Job
		if err := json.Unmarshal(data, &job); err != nil {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *Store) CompleteCodeJob(ctx context.Context, id string, result *JobResult) error {
	return s.SetStatus(ctx, JobKindCode, id, StatusCompleted, result)
}

func (s *Store) FailCodeJob(ctx context.Context, id string, errMsg string) error {
	return s.SetStatus(ctx, JobKindCode, id, StatusFailed, &JobResult{Error: errMsg})
}

func (s *Store) CompleteFilesJob(ctx context.Context, id string, result *JobResult) error {
	return s.SetStatus(ctx, JobKindFiles, id, StatusCompleted, result)
}

func (s *Store) FailFilesJob(ctx context.Context, id string, errMsg string) error {
	return s.SetStatus(ctx, JobKindFiles, id, StatusFailed, &JobResult{Error: errMsg})
}

func (s *Store) RequeueCode(ctx context.Context, id string) error {
	return s.rdb.RPush(ctx, queueCode, id).Err()
}

func (s *Store) RequeueFiles(ctx context.Context, id string) error {
	return s.rdb.RPush(ctx, queueFiles, id).Err()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.rdb.Ping(ctx).Err()
}
