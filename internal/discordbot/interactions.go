package discordbot

import (
	"errors"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

func ephemeral(text string) discord.MessageCreate {
	return discord.NewMessageCreate().WithEphemeral(true).WithContent(text)
}

func followUp(client rest.Rest, app snowflake.ID, token, text string) error {
	_, err := client.CreateFollowupMessage(app, token, ephemeral(text))
	return err
}

// componentInteraction adapts a button or select menu use. It is used by
// one goroutine. responded records a successful initial response: after
// it, an ephemeral notice has to be a follow-up message.
type componentInteraction struct {
	e         *events.ComponentInteractionCreate
	responded bool
}

var _ bot.Interaction = (*componentInteraction)(nil)

func (c *componentInteraction) UserID() snowflake.ID { return c.e.User().ID }
func (c *componentInteraction) CustomID() string     { return c.e.Data.CustomID() }
func (c *componentInteraction) Text() string         { return "" }

func (c *componentInteraction) Values() []string {
	if d, ok := c.e.Data.(discord.StringSelectMenuInteractionData); ok {
		return d.Values
	}
	return nil
}

func (c *componentInteraction) Message() (*discord.Message, bool) { return &c.e.Message, true }

func (c *componentInteraction) Acknowledge() error {
	return c.respond(c.e.DeferUpdateMessage())
}

func (c *componentInteraction) Modal(m discord.ModalCreate) error {
	return c.respond(c.e.Modal(m))
}

func (c *componentInteraction) Ephemeral(text string) error {
	if c.responded {
		return followUp(c.e.Client().Rest, c.e.ApplicationID(), c.e.Token(), text)
	}
	return c.respond(c.e.CreateMessage(ephemeral(text)))
}

func (c *componentInteraction) respond(err error) error {
	c.responded = c.responded || err == nil
	return err
}

// modalInteraction adapts a modal submit; see componentInteraction.
type modalInteraction struct {
	e         *events.ModalSubmitInteractionCreate
	responded bool
}

var _ bot.Interaction = (*modalInteraction)(nil)

func (m *modalInteraction) UserID() snowflake.ID { return m.e.User().ID }
func (m *modalInteraction) CustomID() string     { return m.e.Data.CustomID }
func (m *modalInteraction) Values() []string     { return nil }

func (m *modalInteraction) Text() string {
	text, _ := m.e.Data.OptText(render.TextInputID) // absent means empty
	return text
}

func (m *modalInteraction) Message() (*discord.Message, bool) {
	return m.e.Message, m.e.Message != nil
}

func (m *modalInteraction) Acknowledge() error {
	if m.e.Message != nil {
		return m.respond(m.e.DeferUpdateMessage())
	}
	return m.respond(m.e.DeferCreateMessage(true))
}

func (m *modalInteraction) Modal(discord.ModalCreate) error {
	return errors.New("discordbot: a modal cannot open another modal")
}

func (m *modalInteraction) Ephemeral(text string) error {
	if m.responded {
		return followUp(m.e.Client().Rest, m.e.ApplicationID(), m.e.Token(), text)
	}
	return m.respond(m.e.CreateMessage(ephemeral(text)))
}

func (m *modalInteraction) respond(err error) error {
	m.responded = m.responded || err == nil
	return err
}
