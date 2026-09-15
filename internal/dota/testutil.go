package dota

// FindBySlot возвращает игрока по слоту — удобно в тестах и при разметке.
func (m *Match) FindBySlot(slot int) *Player {
	for _, p := range m.Players {
		if p.Slot == slot {
			return p
		}
	}
	return nil
}

// FindByHero возвращает игрока по имени героя.
func (m *Match) FindByHero(name string) *Player {
	for _, p := range m.Players {
		if p.Name() == name {
			return p
		}
	}
	return nil
}
