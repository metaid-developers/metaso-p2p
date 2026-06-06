package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// MetaFile* codes are used by /info/* endpoints to stay wire-compatible with
// meta-file-system, so idchat's `metafileIndexerApi` client (which treats
// `code === 1` as success and any other code as failure) can consume
// metaso-p2p as a drop-in replacement without TypeScript changes.
const (
	MetaFileSuccessCode      = 1
	MetaFileInvalidParamCode = 40000
	MetaFileNotFoundCode     = 40400
)

// RespSuccess sends a successful JSON response in idchat-compatible format.
func RespSuccess(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code":           0,
		"data":           data,
		"message":        "",
		"processingTime": time.Now().UnixMilli(),
	})
}

// RespSuccessCode sends a successful JSON response with a caller-specified
// success code. Use this only for endpoints that must mirror a third-party
// service's code convention (e.g. /info/* endpoints mirroring meta-file-system
// where success == 1). For all idchat-native endpoints use RespSuccess (code 0).
func RespSuccessCode(c *gin.Context, code int, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code":           code,
		"data":           data,
		"message":        "",
		"processingTime": time.Now().UnixMilli(),
	})
}

// RespErr sends an error JSON response in idchat-compatible format.
// If the caller passes code == 0, it is normalized to 1 so the response is
// always distinguishable from RespSuccess. Non-zero codes pass through
// untouched, which lets the userinfo handlers use codes 40400 / 40000 to
// stay wire-compatible with meta-file-system.
func RespErr(c *gin.Context, code int, message string) {
	if code == 0 {
		code = 1
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    code,
		"message": message,
	})
}
