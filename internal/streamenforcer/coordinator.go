package streamenforcer

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const coordinatorKey = "silo:enforcer:leader"

var acquireLeaseScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v == false or v == ARGV[1] then
	redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
	return 1
end
return 0
`)

// Coordinator elects one enforcer replica for an evaluation pass.
type Coordinator interface {
	Acquire(ctx context.Context) (bool, error)
}

// RedisCoordinator uses a renewable lease to select one evaluator.
type RedisCoordinator struct {
	rdb        *redis.Client
	instanceID string
	leaseTTL   time.Duration
}

// NewRedisCoordinator creates a coordinator whose lease lasts twice the
// evaluation interval.
func NewRedisCoordinator(rdb *redis.Client, instanceID string, interval time.Duration) *RedisCoordinator {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &RedisCoordinator{rdb: rdb, instanceID: instanceID, leaseTTL: 2 * interval}
}

// Acquire claims or renews this instance's lease.
func (c *RedisCoordinator) Acquire(ctx context.Context) (bool, error) {
	if c == nil || c.rdb == nil {
		return true, nil
	}
	result, err := acquireLeaseScript.Run(
		ctx,
		c.rdb,
		[]string{coordinatorKey},
		c.instanceID,
		c.leaseTTL.Milliseconds(),
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
