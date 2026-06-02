# a-kerno — ядро ADE

**a-kerno** — демон-оркестратор, ядро экосистемы ADE.

## Назначение

- Запускается демоном входа [`adm`](../adm/) при старте пользовательской сессии
- Читает декларативный конфиг `~/.config/ade/daemons.md` со списком демонов ADE
- Запускает демоны как дочерние процессы в указанном порядке
- Отслеживает состояние демонов и перезапускает согласно политикам (always, on-failure, once, disabled)
- Предоставляет управляющий Unix-сокет `/tmp/ade-{UID}/kerno` с протоколом CMDLIST (TXT01 / BIN01)
- Поддерживает автозапуск пользовательских программ (XDG autostart + Markdown)
- Выполняет graceful shutdown всех демонов при завершении сессии
- Клиентская библиотека [`client/kerno/`](client/kerno/client.go) для программного доступа

## Конфигурация

Конфигурация демонов хранится в Markdown-формате в файле `~/.config/ade/daemons.md`.
Формат: секции `## <name> properties` с ключами `- key: value` и секция `## enabled daemons`
с task-листом `- [x]` / `- [ ]`.

## Переменные окружения

| Переменная | Назначение | По умолчанию |
|---|---|---|
| `ADE_CONFIG_HOME` | Каталог конфигурации ADE | `~/.config/ade` |
| `ADE_RUNTIME_DIR` | Каталог runtime (сокеты, PID) | `/tmp/ade-{UID}` |
| `ADE_KERNO_SOCK` | Путь к управляющему сокету | `/tmp/ade-{UID}/kerno` |
| `ADE_INDEXD_SOCK` | Сокет a-lancxo | `/tmp/ade-{UID}/indexd` |
| `ADE_SKRIPTO_SOCK` | Сокет a-skripto | `/tmp/ade-{UID}/skripto` |

## Управляющий протокол

Управление демонами производится через Unix-сокет по протоколу CMDLIST:
- **TXT01** — текстовый формат (human-readable, заголовок `TXT01`)
- **BIN01** — бинарный формат (фреймовый, заголовок `BIN1`)

### Команды (12 шт.)

| TXT01 | BIN01 (code) | Назначение |
|---|---|---|
| `list-daemons` | 1 | Список демонов |
| `status <name>` | 2 | Статус демона |
| `restart <name>` | 3 | Перезапуск демона |
| `stop <name>` | 4 | Остановка демона |
| `start <name>` | 5 | Запуск демона |
| `list-features` | 6 | Список фич |
| `logs <name>` | 7 | Логи демона |
| `shutdown` | 8 | Завершение a-kerno |
| `prog-list` | 9 | Список программ |
| `prog-status <name>` | 10 | Статус программы |
| `prog-start <name>` | 11 | Запуск программы |
| `prog-stop <name>` | 12 | Остановка программы |

## Документация

- [Архитектура a-kerno](../doc/a-kerno/architecture.md) — полная спецификация
- [Дизайн пользовательских программ](../doc/a-kerno/user-programs-design.md)
- [Документация в `doc/`](doc/README.md)

## Сборка

```bash
make build      # Сборка
make test       # Тесты
make lint       # Линтер
make tidy       # go mod tidy
make install    # Установка (требует SUDO=sudo)
```

## Структура проекта

```
a-kerno/
├── cmd/a-kerno/main.go              — точка входа, Orchestrator
├── internal/
│   ├── binparser/                   — бинарный протокол BIN01 (Фаза 6)
│   │   ├── parser.go                — фреймовый парсер
│   │   ├── writer.go                — фреймовый writer
│   │   └── parser_test.go           — тесты
│   ├── config/config.go             — Config, Init()
│   ├── daemon/                      — управление демонами
│   ├── feature/registry.go          — реестр фич ADE
│   ├── logdup/                      — дублирование stdout/stderr
│   ├── orchestrator/orchestrator.go — оркестратор
│   ├── program/                     — управление пользовательскими программами
│   └── server/                      — управляющий Unix-сокет (TXT01 + BIN01)
├── client/kerno/client.go           — клиентская библиотека (Фаза 6)
├── parser/                          — парсер TXT01
├── doc/                             — ссылки на документацию
├── go.mod, go.sum, Makefile
└── .gitignore, .golangci.yaml
```
