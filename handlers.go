package kumo

import (
	"github.com/optimiweb/kumo/pkg/handlers"
	"github.com/optimiweb/kumo/pkg/urlnorm"
)

// Handler helpers re-exported from pkg/handlers.
var (
	NewSitemapHandler            = handlers.NewSitemapHandler
	NewLinkDiscoveryHandler      = handlers.NewLinkDiscoveryHandler
	ChainHandlers                = handlers.ChainHandlers
	DefaultSitemapHandlerOptions = handlers.DefaultSitemapHandlerOptions
)

type (
	SitemapHandlerOptions = handlers.SitemapHandlerOptions
	LinkDiscoveryOptions  = handlers.LinkDiscoveryOptions
	NormalizationPolicy   = urlnorm.NormalizationPolicy
	TrailingSlashOption   = urlnorm.TrailingSlashOption
)

var (
	NewNormalizedIdentifier    = urlnorm.NewNormalizedIdentifier
	DefaultNormalizationPolicy = urlnorm.DefaultNormalizationPolicy
)

const (
	TrailingSlashPreserve = urlnorm.TrailingSlashPreserve
	TrailingSlashRemove   = urlnorm.TrailingSlashRemove
	TrailingSlashAdd      = urlnorm.TrailingSlashAdd
)
