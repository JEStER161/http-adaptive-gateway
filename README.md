# Adaptive Routing Gateway

HTTP Gateway для дипломного стенда с несколькими backend-репликами. Gateway строит маршруты из YAML и OpenAPI, выполняет подключённые к операции плагины, выбирает реплику общей для процесса стратегией и проксирует запрос через `httputil.ReverseProxy`.

## Архитектура

Конфигурация разделена на три уровня:

1. [`configs/env.yaml`](configs/env.yaml) — HTTP-сервер, transport, логирование, глобальные плагины и выбранная стратегия.
2. [`configs/routes/services.yaml`](configs/routes/services.yaml) — routes в формате `edge-api`: `name`, selector-prefix `endpoint`, `manifest` и `backend.hosts`. Списки hosts вынесены в YAML anchors вида `backend-*-hosts`.
3. [`docs/manifests`](docs/manifests) — OpenAPI-операции, `x-backend.urlPattern` и упорядоченные `x-plugins`.

`internal/app` вручную собирает registry стратегий, plugin registry, observer, shared HTTP transport, proxy и Echo router. База данных и service discovery Gateway не требуются.

## Маршрутизация пути

Внешний путь составляется как `route.endpoint + OpenAPI path`, а upstream path берётся только из `x-backend[0].urlPattern`.

```text
GET /delivery/v3/records -> service delivery -> /v3/records
GET /api/v1/items       -> service api      -> /api/v1/items
```

Параметры OpenAPI вида `{id}` регистрируются в Echo как `:id` и безопасно подставляются в `urlPattern`. Query string, HTTP-метод и body сохраняются.

Каждая операция обязана последним элементом подключить глобальный balancer:

```yaml
x-plugins:
  - name: balancer
    inheritGlobal: true
    inheritFromAlias: gateway-balancer
```

Отсутствующий, повторный или не последний balancer блокирует запуск.

## Балансировка

Стратегия задаётся только в `application.routePluginsGlobal` основного конфига и одинакова для всех сервисов. Runtime-состояние изолировано: у каждого сервиса свои счётчики, in-flight значения и hash ring.

Поддерживаются:

- `round_robin` — циклический выбор;
- `random` — случайный выбор, опционально с воспроизводимым seed;
- `least_connections` — минимум активных запросов, Round Robin при равенстве;
- `consistent_hash` — consistent hash ring по заголовку или IP клиента.

Конфигурация `adaptive` и её метрик уже строго валидируется. Активировать её пока нельзя: Gateway завершит запуск с сообщением, что стратегия будет реализована на следующем этапе. Добавление метрик изолировано metric/factory registry и не требует изменения proxy.

Чтобы сравнить стратегии, измените только `application.routePluginsGlobal[0].params.strategy` и перезапустите Gateway.

## Плагины

Локальный `rate_limit` реализован как конкурентно-безопасный in-memory Token Bucket одного экземпляра Gateway. Он поддерживает `remote_ip`, возвращает `429` и `Retry-After` и выполняется до выбора upstream. Один component definition переиспользуется всеми ссылающимися на него операциями.

Авторизация пока не реализована; все маршруты доступны без токена.

## Запуск с Backend-service

Сначала соберите и запустите PostgreSQL и три backend-реплики:

```bash
cd ../Backend-service
docker compose -f deployments/local/docker-compose.yml up --build
```

Затем из каталога Gateway:

```bash
go run ./cmd/bin -config configs/env.yaml
```

Проверка:

```bash
curl http://localhost:8080/health

curl -i -X POST http://localhost:8080/api/v1/items \
  -H 'Content-Type: application/json' \
  -d '{"name":"example","value":"through gateway"}'
```

В последовательных ответах `X-Gateway-Upstream` меняется между `api-1`, `api-2`, `api-3`. Instance ID формируется из `endpoint` и порядкового номера host.

Для запуска Gateway в Docker:

```bash
docker compose -f deployments/local/docker-compose.yml up --build
```

Compose монтирует [`deployments/envs/services-docker.yaml`](deployments/envs/services-docker.yaml), где backend-реплики доступны через `host.docker.internal`. Для другой сети контейнеров замените hosts в этом файле на их DNS-имена.

## Ошибки и логи

Ошибки Gateway имеют единый вид:

```json
{
  "error": {
    "code": "upstream_unavailable",
    "message": "backend service is temporarily unavailable",
    "request_id": "..."
  }
}
```

JSON-логи содержат request, route, service и instance ID, список плагинов, status и длительность. Тела запросов и чувствительные заголовки не логируются.

## Проверки

```bash
gofmt -w cmd configs delivery internal pkg
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
```

Тесты покрывают строгую конфигурацию, независимость стратегий, concurrent Round Robin, Least Connections, Consistent Hash, компиляцию manifest, rewrite пути, сохранение query/body, observer, `404`/`405`, health check и отказ rate limiter до upstream.

## Ограничения

- Конфигурация читается только при запуске.
- Нет retries, circuit breaker, service discovery и OpenAPI request/response validation.
- Rate limiter локален для одного процесса.
- Adaptive selection пока зарезервирован конфигурацией и интерфейсами, но не реализован.
