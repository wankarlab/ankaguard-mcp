package main

import (
	"github.com/gin-gonic/gin"
)

// legacyRouteMounter is a handler registered directly on the REST engine
// (:8081) by main.go, without the JWT middleware.
type legacyRouteMounter interface {
	RegisterRoutesGin(router gin.IRouter)
}

// mountLegacyRoutes mounts the legacy analysis/monitoring/knowledge/
// validation/strategy/metrics handlers. They have no authentication and some
// of them mutate state (strategies CRUD, knowledge reload, cache clean), so
// the product edition does not mount them at all. The standard build keeps
// them exactly as before (listed as Pending in internal/routepolicy).
// It returns the number of handlers mounted.
func mountLegacyRoutes(router gin.IRouter, p productProfile, handlers ...legacyRouteMounter) int {
	if p.Product {
		return 0
	}
	n := 0
	for _, h := range handlers {
		if h == nil {
			continue
		}
		h.RegisterRoutesGin(router)
		n++
	}
	return n
}

// productGuarded returns where to mount routes that the standard build serves
// without a token (fee ledger, strategy validation): the engine itself in the
// standard build (unchanged behaviour), and a group behind the JWT middleware
// in the product edition. A nil jwt (ENABLE_JWT_AUTH=false) leaves the routes
// open in both builds, like every other route.
func productGuarded(router gin.IRouter, p productProfile, jwt gin.HandlerFunc) gin.IRouter {
	if !p.Product || jwt == nil {
		return router
	}
	return router.Group("", jwt)
}
