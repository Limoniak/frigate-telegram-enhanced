// Package telegram est un client minimal de l'API Bot Telegram.
package telegram

import (
	"context"
	"sync"
	"time"
)

// Limiter espace les envois pour respecter les limites de Telegram :
// environ 30 messages/s au total, 1/s par chat privé, 20/min par groupe.
// Chaque chat a son propre créneau : un chat lent ne retarde pas les autres.
type Limiter struct {
	mu                     sync.Mutex
	global, private, group time.Duration
	nextGlobal             time.Time
	nextChat               map[int64]time.Time
	now                    func() time.Time
}

func NewLimiter() *Limiter { return NewLimiterWith(time.Second/30, time.Second, 3*time.Second) }

func NewLimiterWith(global, private, group time.Duration) *Limiter {
	return &Limiter{global: global, private: private, group: group, nextChat: map[int64]time.Time{}, now: time.Now}
}

// Wait bloque jusqu'à ce qu'un envoi vers chatID soit autorisé.
func (l *Limiter) Wait(ctx context.Context, chatID int64) error {
	if err := sleep(ctx, l.reserveChat(chatID)); err != nil {
		return err
	}
	return sleep(ctx, l.reserveGlobal())
}

func (l *Limiter) reserveChat(chatID int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	at := now
	if next := l.nextChat[chatID]; next.After(at) {
		at = next
	}
	interval := l.private
	if chatID < 0 {
		interval = l.group
	}
	l.nextChat[chatID] = at.Add(interval)
	return at.Sub(now)
}

func (l *Limiter) reserveGlobal() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	at := now
	if l.nextGlobal.After(at) {
		at = l.nextGlobal
	}
	l.nextGlobal = at.Add(l.global)
	return at.Sub(now)
}

// sleep attend d ou l'annulation de ctx.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
