package bothomepage

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

func (a *Aggregator) handleGlobalMetaID(c *gin.Context) {
	opts, err := ParseOptions(c.Request.URL.Query())
	if err != nil {
		api.RespErr(c, 40000, "invalid parameter")
		return
	}

	if opts.Version == "v3" {
		data, err := a.BuildV3(c.Param("globalMetaId"), opts)
		if err != nil {
			respondBuildError(c, err)
			return
		}
		api.RespSuccess(c, data)
		return
	}

	data, err := a.Build(c.Param("globalMetaId"), opts)
	if err != nil {
		respondBuildError(c, err)
		return
	}

	api.RespSuccess(c, data)
}

func respondBuildError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidParameter):
		api.RespErr(c, 40000, "invalid parameter")
	case errors.Is(err, ErrNotFound):
		api.RespErr(c, 40400, "bot homepage not found")
	default:
		api.RespErr(c, 50000, "aggregation unavailable")
	}
}
