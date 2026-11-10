package tgsetup

import (
	"context"
	"fmt"
	"strings"
)

// EnableBotToBot turns on Bot-to-Bot Communication Mode for a bot this account owns.
// @BotFather no longer puts that switch on the Bot Settings keyboard. The switch lives in
// the BotFather Mini App, and the chat command /setbot2bot still toggles it with buttons.
// A bot this account does not own returns ErrNotOwner.
func EnableBotToBot(ctx context.Context, m Menu, username string) error {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return fmt.Errorf("a bot username is required")
	}
	_, _ = say(ctx, m, "/cancel")
	after, err := m.Send(ctx, "/setbot2bot")
	if err != nil {
		return err
	}
	text, msgID, buttons, err := m.Marked(ctx, after)
	if err != nil {
		return err
	}
	if err := classify(text); err != nil && len(buttons) == 0 {
		return err
	}
	if botTalkOn(text, buttons) {
		return nil
	}
	if on := matchButton(buttons, enableScore); on != nil {
		return pressEnable(ctx, m, msgID, on.Data)
	}
	for page := 0; page < 6; page++ {
		if b := matchButton(buttons, func(s string) int {
			if strings.Contains(strings.ToLower(s), strings.ToLower(username)) {
				return 2
			}
			return 0
		}); b != nil {
			text, msgID, buttons, err = m.Press(ctx, msgID, b.Data)
			if err != nil {
				return err
			}
			return turnBotTalkOn(ctx, m, text, msgID, buttons)
		}
		next := findNav(buttons, true)
		if next == nil || len(next.Data) == 0 {
			break
		}
		text, msgID, buttons, err = m.Press(ctx, msgID, next.Data)
		if err != nil {
			return err
		}
	}
	if pickerShown(text, buttons) {
		return ErrNotOwner
	}
	after, err = m.Send(ctx, "@"+username)
	if err != nil {
		return err
	}
	text, msgID, buttons, err = m.Marked(ctx, after)
	if err != nil {
		return err
	}
	return turnBotTalkOn(ctx, m, text, msgID, buttons)
}

func turnBotTalkOn(ctx context.Context, m Menu, text string, msgID int, buttons []Button) error {
	if err := classify(text); err != nil && len(buttons) == 0 {
		return err
	}
	if botTalkOn(text, buttons) {
		return nil
	}
	if on := matchButton(buttons, enableScore); on != nil {
		return pressEnable(ctx, m, msgID, on.Data)
	}
	// Older BotFather still nests the switch under Bot Settings.
	if settings := matchButton(buttons, settingsScore); settings != nil {
		text, msgID, buttons, err := m.Press(ctx, msgID, settings.Data)
		if err != nil {
			return err
		}
		return enableFromSettings(ctx, m, text, msgID, buttons)
	}
	return fmt.Errorf("@BotFather has no bot-to-bot button: %s", firstLine(text))
}

func pressEnable(ctx context.Context, m Menu, msgID int, data []byte) error {
	text, _, _, err := m.Press(ctx, msgID, data)
	if err != nil {
		return err
	}
	return classify(text)
}

func pickerShown(text string, buttons []Button) bool {
	found, rest := ownedFrom(text, buttons)
	return len(found) > 0 || len(rest) > 0
}

func enableFromSettings(ctx context.Context, m Menu, text string, msgID int, buttons []Button) error {
	if err := classify(text); err != nil && len(buttons) == 0 {
		return err
	}
	talk := matchButton(buttons, botTalkScore)
	if talk == nil {
		return fmt.Errorf("@BotFather has no bot-to-bot button: %s", firstLine(text))
	}
	text, msgID, buttons, err := m.Press(ctx, msgID, talk.Data)
	if err != nil {
		return err
	}
	if botTalkOn(text, buttons) {
		return nil
	}
	on := matchButton(buttons, enableScore)
	if on == nil {
		return fmt.Errorf("@BotFather did not offer to turn bot-to-bot on: %s", firstLine(text))
	}
	text, _, _, err = m.Press(ctx, msgID, on.Data)
	if err != nil {
		return err
	}
	if err := classify(text); err != nil {
		return err
	}
	return nil
}

func matchButton(buttons []Button, score func(string) int) *Button {
	best, n := 0, -1
	for i := range buttons {
		if len(buttons[i].Data) == 0 {
			continue
		}
		s := score(buttons[i].Text)
		if s > best {
			best, n = s, i
		}
	}
	if n < 0 {
		return nil
	}
	return &buttons[n]
}

func settingsScore(text string) int {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "bot setting"), strings.Contains(t, "تنظیمات ربات"):
		return 3
	case t == "settings", strings.Contains(t, "تنظیمات"):
		return 1
	default:
		return 0
	}
}

func botTalkScore(text string) int {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "bot-to-bot"), strings.Contains(t, "bot to bot"), strings.Contains(t, "bot2bot"),
		strings.Contains(t, "ربات به ربات"), strings.Contains(t, "ربات‌به‌ربات"):
		return 3
	case strings.Contains(t, "communication") && strings.Contains(t, "bot"):
		return 2
	default:
		return 0
	}
}

func enableScore(text string) int {
	t := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(t, "disable") || strings.Contains(t, "turn off") || strings.Contains(t, "خاموش") {
		return 0
	}
	switch {
	case t == "enable" || t == "turn on" || strings.HasPrefix(t, "enable ") || strings.Contains(t, "روشن کردن") || strings.Contains(t, "فعال کردن"):
		return 3
	case strings.Contains(t, "enable") || t == "روشن" || t == "فعال":
		return 2
	default:
		return 0
	}
}

func botTalkOn(text string, buttons []Button) bool {
	t := strings.ToLower(text)
	if strings.Contains(t, "enabled") || strings.Contains(t, "is on") || strings.Contains(t, "روشن است") || strings.Contains(t, "فعال است") {
		return true
	}
	for _, b := range buttons {
		low := strings.ToLower(b.Text)
		if strings.Contains(low, "disable") || strings.Contains(low, "turn off") || strings.Contains(low, "خاموش") {
			return true
		}
	}
	return false
}
