package gateway

import (
	"fmt"
	"sync/atomic"
	"time"

	"frappe_lightning/config"

	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

// UpstreamPool holds the list of upstream worker URLs for one site and
// selects them via atomic round-robin.
type UpstreamPool struct {
	workers []string
	counter atomic.Uint64
}

// NewUpstreamPool creates an UpstreamPool from a list of worker base URLs.
func NewUpstreamPool(workers []string) *UpstreamPool {
	return &UpstreamPool{workers: workers}
}

// Next returns the next upstream URL in round-robin order.
func (p *UpstreamPool) Next() string {
	idx := p.counter.Add(1) % uint64(len(p.workers))
	return p.workers[idx]
}

// Len returns the number of upstream workers.
func (p *UpstreamPool) Len() int { return len(p.workers) }

// Proxy performs the actual HTTP round-trip to the upstream worker.
// It copies the incoming fasthttp request, sets the upstream host,
// executes the request, and writes the response back to the caller's buffer.
type Proxy struct {
	pools   map[string]*UpstreamPool   // keyed by site name
	breakers map[string]*CircuitBreaker // keyed by site name
	client  *fasthttp.Client
	log     *zap.Logger
}

// NewProxy creates a Proxy from the gateway site configs.
func NewProxy(sites []config.GatewaySite, cbCfg config.CircuitBreakerCfg, log *zap.Logger) *Proxy {
	pools := make(map[string]*UpstreamPool, len(sites))
	breakers := make(map[string]*CircuitBreaker, len(sites))

	openDur := time.Duration(cbCfg.OpenDurationSec) * time.Second
	probeInterval := time.Duration(cbCfg.HalfOpenProbeInterval) * time.Second

	for _, s := range sites {
		if len(s.UpstreamWorkers) == 0 {
			continue
		}
		pools[s.Name] = NewUpstreamPool(s.UpstreamWorkers)
		breakers[s.Name] = NewCircuitBreaker(cbCfg.FailureThreshold, openDur, probeInterval, log)
	}

	return &Proxy{
		pools:    pools,
		breakers: breakers,
		client: &fasthttp.Client{
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			MaxConnWaitTimeout: 5 * time.Second,
		},
		log: log,
	}
}

// Forward proxies an incoming fasthttp request to the upstream for the given site
// and writes the upstream response into resp.
// Returns an error when the circuit is open or the upstream call fails.
func (p *Proxy) Forward(site string, req *fasthttp.Request, resp *fasthttp.Response) error {
	pool, ok := p.pools[site]
	if !ok || pool.Len() == 0 {
		return fmt.Errorf("no upstream workers configured for site %q", site)
	}

	cb := p.breakers[site]
	if !cb.Allow() {
		return fmt.Errorf("circuit open for site %q — upstream is unhealthy", site)
	}

	upstream := pool.Next()

	// Build the target URI: upstream base + original request path + query.
	targetURI := upstream + string(req.RequestURI())
	req.SetRequestURIBytes([]byte(targetURI))

	// Strip hop-by-hop headers that must not be forwarded.
	req.Header.Del("Connection")
	req.Header.Del("Keep-Alive")
	req.Header.Del("Proxy-Authenticate")
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("TE")
	req.Header.Del("Trailers")
	req.Header.Del("Transfer-Encoding")
	req.Header.Del("Upgrade")

	if err := p.client.Do(req, resp); err != nil {
		cb.RecordFailure()
		p.log.Error("upstream request failed",
			zap.String("site", site),
			zap.String("upstream", upstream),
			zap.Error(err),
		)
		return err
	}

	if resp.StatusCode() >= 500 {
		cb.RecordFailure()
	} else {
		cb.RecordSuccess()
	}
	return nil
}

// BreakerState returns the circuit breaker state string for a site.
func (p *Proxy) BreakerState(site string) string {
	if cb, ok := p.breakers[site]; ok {
		return cb.State()
	}
	return "unknown"
}
