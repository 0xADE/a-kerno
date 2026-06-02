# a-kerno documentation

## Основные документы

- [**architecture.md**](../../doc/a-kerno/architecture.md) — архитектура a-kerno, протоколы CMDLIST TXT01 и BIN01, диаграммы взаимодействия
- [**user-programs-design.md**](../../doc/a-kerno/user-programs-design.md) — дизайн подсистемы пользовательских программ (Phase 5)

## Протоколы

### TXT01 (текстовый CMDLIST)

Формат: `TXT01\n` заголовок → команды и значения в текстовом виде → ответы с кодами 20 (OK) и 50 (ERROR).

### BIN01 (бинарный CMDLIST)

Фреймовый протокол: `| type (1B) | length (4B LE) | payload |`. Типы фреймов: CMD, ATTR, DATA, END. Коды команд: 1B. Типы значений: STR, INT, BOOL, BLOB.

Детали: см. [architecture.md § Бинарный протокол BIN01](../../doc/a-kerno/architecture.md).

## Клиентская библиотека

```go
import "github.com/0xADE/a-kerno/client/kerno"

// Текстовый режим:
client, _ := kerno.NewClient("/tmp/ade-1000/kerno")
defer client.Close()
daemons, _ := client.ListDaemons()

// Бинарный режим:
client, _ := kerno.NewBinaryClient("/tmp/ade-1000/kerno")
defer client.Close()
status, _ := client.Status("a-lancxo")
```
