package web

import (
	"mime"
	"net/http"

	"github.com/beego/beego/v2/core/logs"
	"github.com/beego/beego/v2/server/web/context"
)

// register MIME type with content type
func registerMime() error {
	for k, v := range mimemaps {
		mime.AddExtensionType(k, v)
	}
	return nil
}

// register default error http handlers, 404,401,403,500 and 503.
func registerDefaultErrorHandler() error {
	m := map[string]func(http.ResponseWriter, *http.Request){
		"401": unauthorized,
		"402": paymentRequired,
		"403": forbidden,
		"404": notFound,
		"405": methodNotAllowed,
		"500": internalServerError,
		"501": notImplemented,
		"502": badGateway,
		"503": serviceUnavailable,
		"504": gatewayTimeout,
		"417": invalidxsrf,
		"422": missingxsrf,
		"413": payloadTooLarge,
	}
	for e, h := range m {
		if _, ok := ErrorMaps[e]; !ok {
			ErrorHandler(e, h)
		}
	}
	return nil
}

func registerSession() error {
	context.SetTrustedProxies(BConfig.WebConfig.Session.TrustedProxies)
	if BConfig.WebConfig.Session.SessionOn {
		getIdentitySessionEngine().ensureTable()
	}
	return nil
}

func registerTemplate() error {
	defer lockViewPaths()
	if err := AddViewPath(BConfig.WebConfig.ViewsPath); err != nil {
		if BConfig.RunMode == DEV {
			logs.Warn(err)
		}
		return err
	}
	return nil
}

func registerGzip() error {
	if BConfig.EnableGzip {
		context.InitGzip(
			AppConfig.DefaultInt("gzipMinLength", -1),
			AppConfig.DefaultInt("gzipCompressLevel", -1),
			AppConfig.DefaultStrings("includedMethods", []string{"GET"}),
		)
	}
	return nil
}
