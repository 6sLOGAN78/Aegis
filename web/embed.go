package web

import "embed"

// DashboardDist embeds the production dashboard distribution assets.
//
//go:embed all:dashboard/dist
var DashboardDist embed.FS
