package auth

// 账号调度优先级（issue #358）：优先级高的账号严格先调度，同优先级内再按
// 健康档位与调度分竞争；账号不可用（冷却/暂停/限额）时自然回落到低优先级。
// 典型用法：官方账号设正值、API-Key 中转渠道保持默认或设负值，实现
// 「官方账号用尽才落中转」的兜底编排。
const (
	minSchedulerPriority int64 = -100
	maxSchedulerPriority int64 = 100
)

func normalizeSchedulerPriority(priority int64) int64 {
	switch {
	case priority < minSchedulerPriority:
		return minSchedulerPriority
	case priority > maxSchedulerPriority:
		return maxSchedulerPriority
	default:
		return priority
	}
}

func (a *Account) SetSchedulerPriority(priority int64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.SchedulerPriority = normalizeSchedulerPriority(priority)
	a.mu.Unlock()
}

func (a *Account) schedulerPriority() int64 {
	if a == nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return normalizeSchedulerPriority(a.SchedulerPriority)
}

func (a *Account) GetSchedulerPriority() int64 {
	return a.schedulerPriority()
}

// ApplyAccountSchedulerPriority 动态更新账号调度优先级（nil 恢复默认 0）。
func (s *Store) ApplyAccountSchedulerPriority(dbID int64, priority *int64) bool {
	acc := s.FindByID(dbID)
	if acc == nil {
		return false
	}
	if priority == nil {
		acc.SetSchedulerPriority(0)
	} else {
		acc.SetSchedulerPriority(*priority)
	}
	s.fastSchedulerUpdate(acc)
	return true
}
