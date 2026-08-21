package captcha

// wire providers。

import (
	"github.com/google/wire"
)

// ProviderSet captcha providers。
var ProviderSet = wire.NewSet(New, NewStoreCaptchaService)
