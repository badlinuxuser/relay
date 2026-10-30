package tui

import "time"

// Маскот живёт по событиям интерфейса:
//
//	calm      ждёт и слушает
//	noticed   заметила: вызов инструмента, смена ключа, подключение
//	surprise  удивление: ошибка, нужно подтверждение, пробуждение
//	joy       успех: ответ получен, режим сменён
//	tired     лимит ключа исчерпан (до первого успеха) или долго нет активности (спит)
type mascot struct {
	base     string
	mood     string
	until    time.Time
	say      string
	sayUntil time.Time
	last     time.Time
}

func newMascot() mascot { return mascot{base: "calm", last: time.Now()} }

// react включает эмоцию на время d и, если say не пусто, показывает реплику.
func (m *mascot) react(mood string, d time.Duration, say string) {
	now := time.Now()
	m.mood = mood
	m.until = now.Add(d)
	if say != "" {
		m.say = say
		sd := d
		if sd < 4*time.Second {
			sd = 4 * time.Second
		}
		m.sayUntil = now.Add(sd)
	}
}

func (m *mascot) asleep(now time.Time) bool {
	return m.base == "calm" && now.Sub(m.last) > 2*time.Minute
}

// touch отмечает активность пользователя; если маскот спал, он удивляется.
func (m *mascot) touch(now time.Time) {
	if m.asleep(now) {
		m.react("surprise", time.Second, "Oh! I wasn't asleep")
	}
	m.last = now
}

func (m *mascot) frame(now time.Time) string {
	if now.Before(m.until) {
		return m.mood
	}
	if m.base != "calm" {
		return m.base
	}
	if m.asleep(now) {
		return "tired"
	}
	return "calm"
}

var moodLabel = map[string]string{
	"calm":     "calm",
	"joy":      "joy",
	"surprise": "surprise",
	"noticed":  "noticed",
	"tired":    "tired",
}

func (m *mascot) caption(now time.Time) (label, say string) {
	f := m.frame(now)
	label = moodLabel[f]
	if f == "tired" && m.asleep(now) && !now.Before(m.until) {
		label = "asleep"
	}
	if now.Before(m.sayUntil) {
		say = m.say
	}
	return label, say
}
