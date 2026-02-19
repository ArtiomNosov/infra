# Sandbox Code Executor (по ТЗ)

Система безопасного исполнения кода по техническому заданию. Используется **Piston** как бэкенд исполнения (без Firecracker/Kata).

## Возможности

- Синхронное исполнение: `POST /execute` (макс. 30 сек)
- Асинхронные задачи: `POST /jobs/code` (inline код, до 5 мин), `POST /jobs/files` (файлы + команда)
- Окружения: `GET /environments` — список runtimes из Piston
- Очереди и хранение результатов в **Redis**
- Лимиты по ТЗ: память, таймауты, размер вывода 2 MB, файлы до 20 шт., 1 MB/файл, 5 MB суммарно
- Health check и статистика: `GET /health`, `GET /stats`

## API (сводка по ТЗ)

| Method | Endpoint | Описание |
|--------|----------|----------|
| GET | /health | Health check |
| GET | /stats | Статистика (задачи, воркеры) |
| GET | /environments | Список окружений (runtimes) |
| POST | /environments | Создать окружение |
| PUT | /environments/:id | Обновить окружение |
| DELETE | /environments/:id | Удалить окружение |
| POST | /execute | Синхронное исполнение (до 30 сек) |
| POST | /jobs/code | Асинхронная задача (inline код) |
| GET | /jobs/code | Список задач code |
| GET | /jobs/code/:id | Статус задачи |
| DELETE | /jobs/code/:id | Отмена задачи |
| POST | /jobs/files | Асинхронная задача (файлы) |
| GET | /jobs/files | Список задач files |
| GET | /jobs/files/:id | Статус задачи |
| DELETE | /jobs/files/:id | Отмена задачи |

## Конфигурация (переменные окружения)

| Переменная | По умолчанию | Описание |
|------------|--------------|----------|
| API_PORT | 8080 | Порт HTTP API |
| WORKER_POOL_SIZE | 10 | Число воркеров |
| DEFAULT_TIMEOUT | 30 | Таймаут по умолчанию (сек) |
| MAX_TIMEOUT | 300 | Макс. таймаут асинхронных задач (сек) |
| QUEUE_TIMEOUT | 300 | Таймаут очереди (сек) |
| REDIS_URL | redis://localhost:6379 | URL Redis |
| PISTON_URL | http://localhost:2000 | URL Piston API |
| STDOUT_MAX_SIZE | 2097152 | Лимит stdout/stderr (2 MB) |
| MEMORY_LIMIT | 268435456 | Лимит памяти (256 MB, для справки) |
| DEBUG | 0 | 1 — режим отладки |

## Запуск

### Локально

1. Запустить Redis и Piston (например, отдельно или через docker-compose только для них).
2. Установить зависимости и запустить:

```bash
make deps
make run
```

Или с явными переменными:

```bash
REDIS_URL=redis://localhost:6379 PISTON_URL=http://localhost:2000 make run
```

### Docker Compose (рекомендуется)

Запускает Redis, Piston и API Gateway на порту 8080:

```bash
docker compose up -d
```

**Если образы долго тянутся** (например, `redis:7-alpine` или `piston`):

- Один раз заранее подтянуть образы:  
  `docker compose pull`  
  затем `docker compose up -d`.
- **Redis уже на хосте (порт 6379):** запуск без контейнера Redis — только Piston и API:  
  `docker compose -f docker-compose.no-redis.yml up -d`  
  Приложение подключается к Redis на хосте по `host.docker.internal:6379`.

**Ошибка 500 при получении образа** (например, `sandbox-code-executor-sandbox-code-executor`): образ задан как `sandbox-code-executor:latest`. Если ошибка остаётся, очистите старые образы и пересоберите:
```bash
docker compose down
docker rmi sandbox-code-executor-sandbox-code-executor 2>/dev/null
docker rmi sandbox-code-executor:latest 2>/dev/null
docker compose build --no-cache && docker compose up -d
```
При необходимости перезапустите Docker Desktop.

Проверка:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/stats
curl http://localhost:8080/environments
```

### Масштабирование воркеров

В docker-compose можно масштабировать только сам сервис (каждый контейнер — свой процесс с пулом воркеров):

```bash
docker compose up -d --scale sandbox-code-executor=2
```

Число воркеров внутри одного процесса задаётся `WORKER_POOL_SIZE`.

## Примеры

### Синхронное исполнение

```bash
curl -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"lang":"python","code":"print(2+2)","timeout":10}'
```

### Асинхронная задача (code)

```bash
curl -X POST http://localhost:8080/jobs/code \
  -H "Content-Type: application/json" \
  -d '{"lang":"python","code":"print(1+1)","timeout":60}'
# Ответ: {"id":"<uuid>","status":"pending"}

curl http://localhost:8080/jobs/code/<id>
```

### Асинхронная задача (files)

```bash
curl -X POST http://localhost:8080/jobs/files \
  -H "Content-Type: application/json" \
  -d '{
    "lang":"python",
    "files":[{"name":"main.py","content":"print(42)"}],
    "timeout":60
  }'
```

## Безопасность и изоляция

Исполнение кода выполняется в **Piston** (изоляция через его окружение). Firecracker/Kata по ТЗ в данной реализации не используются.

## Логирование

Формат логов: JSON (zap). Уровни: INFO, WARN, ERROR. Просмотр: `docker compose logs sandbox-code-executor`.
