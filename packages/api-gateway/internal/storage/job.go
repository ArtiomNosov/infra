package storage

import (
	"context"
	"encoding/json"
	"time"
	"github.com/redis/go-redis/v9"
)

const (
	jobKeyPrefix      = "job:"
	queueCodeKey      = "queue:jobs:code"
	queueFilesKey     = "queue:jobs:files"
	jobTTL            = 14400
)

type JobType string

const (
	JobTypeCode  JobType = "code"
	JobTypeFiles JobType = "files"
)

type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
	StatusTimeout   JobStatus = "timeout"
	StatusCancelled JobStatus = "cancelled"
)

type JobResult struct {
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExecutionTimeMs int64  `json:"execution_time_ms"`
}

type Job struct {
	JobID      string     `json:"job_id"`
	Type       JobType    `json:"type"`
	Environment string    `json:"environment"`
	Status     JobStatus  `json:"status"`
	Code       string     `json:"code,omitempty"`
	Files      []JobFile  `json:"files,omitempty"`
	Command    string     `json:"command,omitempty"`
	Stdin      string     `json:"stdin"`
	Timeout    int        `json:"timeout"`
	Network    bool       `json:"network"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	WorkerID   string     `json:"worker_id,omitempty"`
	Result     *JobResult `json:"result,omitempty"`
}

type JobFile struct {
	Name     string `json:"name"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type JobStorage struct {
	rdb *redis.Client
}

func NewJobStorage(rdb *redis.Client) *JobStorage {
	return &JobStorage{rdb: rdb}
}

func (s *JobStorage) Create(ctx context.Context, job *Job) error {
	key := jobKeyPrefix + job.JobID

	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	if err := s.rdb.HSet(ctx, key, "data", string(data)).Err(); err != nil {
		return err
	}

	if err := s.rdb.Expire(ctx, key, time.Duration(jobTTL)*time.Second).Err(); err != nil {
		return err
	}

	var queueKey string
	if job.Type == JobTypeCode {
		queueKey = queueCodeKey
	} else {
		queueKey = queueFilesKey
	}

	return s.rdb.LPush(ctx, queueKey, job.JobID).Err()
}

func (s *JobStorage) Get(ctx context.Context, jobID string) (*Job, error) {
	key := jobKeyPrefix + jobID
	data, err := s.rdb.HGet(ctx, key, "data").Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var job Job
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return nil, err
	}

	return &job, nil
}

func (s *JobStorage) Update(ctx context.Context, job *Job) error {
	key := jobKeyPrefix + job.JobID

	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	return s.rdb.HSet(ctx, key, "data", string(data)).Err()
}

func (s *JobStorage) List(ctx context.Context, jobType JobType, statusFilter, envFilter string, limit, offset int) ([]*Job, int, error) {
	var queueKey string
	if jobType == JobTypeCode {
		queueKey = queueCodeKey
	} else {
		queueKey = queueFilesKey
	}

	allJobIDs, err := s.rdb.LRange(ctx, queueKey, 0, -1).Result()
	if err != nil {
		return nil, 0, err
	}

	var jobs []*Job
	for _, jobID := range allJobIDs {
		job, err := s.Get(ctx, jobID)
		if err != nil {
			continue
		}
		if job == nil {
			continue
		}

		if statusFilter != "" && string(job.Status) != statusFilter {
			continue
		}
		if envFilter != "" && job.Environment != envFilter {
			continue
		}

		jobs = append(jobs, job)
	}

	total := len(jobs)

	start := offset
	if start > len(jobs) {
		start = len(jobs)
	}
	end := start + limit
	if end > len(jobs) {
		end = len(jobs)
	}

	if start < end {
		jobs = jobs[start:end]
	} else {
		jobs = []*Job{}
	}

	return jobs, total, nil
}

func (s *JobStorage) Cancel(ctx context.Context, jobID string) error {
	job, err := s.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}

	if job.Status == StatusCompleted || job.Status == StatusFailed || job.Status == StatusTimeout {
		return nil
	}

	job.Status = StatusCancelled
	return s.Update(ctx, job)
}

func (s *JobStorage) FindByEnvironment(ctx context.Context, envID string, statuses []JobStatus) ([]*Job, error) {
	var queueKeys []string
	queueKeys = append(queueKeys, queueCodeKey, queueFilesKey)

	var allJobIDs []string
	for _, queueKey := range queueKeys {
		jobIDs, err := s.rdb.LRange(ctx, queueKey, 0, -1).Result()
		if err != nil {
			continue
		}
		allJobIDs = append(allJobIDs, jobIDs...)
	}

	var jobs []*Job
	for _, jobID := range allJobIDs {
		job, err := s.Get(ctx, jobID)
		if err != nil {
			continue
		}
		if job == nil {
			continue
		}

		if job.Environment != envID {
			continue
		}

		for _, status := range statuses {
			if job.Status == status {
				jobs = append(jobs, job)
				break
			}
		}
	}

	return jobs, nil
}

func (s *JobStorage) GetClient() *redis.Client {
	return s.rdb
}

