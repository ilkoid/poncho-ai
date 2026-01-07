// Package agent предоставляет простой API для создания и запуска AI агентов.
//
// Пакет agent является фасадом над pkg/chain, предоставляя более удобный API
// для создания агентов. Интерфейс Agent определён в pkg/chain для избежания
// циклических импортов.
package agent

import (
	"github.com/ilkoid/poncho-ai/pkg/chain"
)

// Agent - это переэкспорт интерфейса из pkg/chain.
//
// Переэкспорт выполняется для обратной совместимости и удобства использования:
//   import "github.com/ilkoid/poncho-ai/pkg/agent"
//
//   var a agent.Agent = ...
//
// Оригинальный интерфейс определён в pkg/chain.Agent.
type Agent = chain.Agent
