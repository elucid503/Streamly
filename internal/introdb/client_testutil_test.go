package introdb

import (
	"time"

	"github.com/imroc/req/v3"
)

func reqPlainClient() *req.Client {
	return req.C().SetTimeout(5 * time.Second)
}