package settings

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/theme"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"net/http"
)

// RegisterTemplateStatic serves revision-scoped compiled theme assets.
func RegisterTemplateStatic(mux *khttp.Server) {
	mux.HandlePrefix("/templates/", http.HandlerFunc(theme.ServeStatic))
}
