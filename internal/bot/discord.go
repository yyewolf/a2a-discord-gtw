package bot

import (
	"errors"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// Discord is the part of DisGo's REST client the gateway uses. rest.Rest
// implements it; tests use a fake.
type Discord interface {
	GetChannel(channelID snowflake.ID, opts ...rest.RequestOpt) (discord.Channel, error)
	GetGuildChannels(guildID snowflake.ID, opts ...rest.RequestOpt) ([]discord.GuildChannel, error)
	CreateGuildChannel(guildID snowflake.ID, guildChannelCreate discord.GuildChannelCreate, opts ...rest.RequestOpt) (discord.GuildChannel, error)
	CreateMessage(channelID snowflake.ID, messageCreate discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
	UpdateMessage(channelID snowflake.ID, messageID snowflake.ID, messageUpdate discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error)
	CreateThreadFromMessage(channelID snowflake.ID, messageID snowflake.ID, threadCreateFromMessage discord.ThreadCreateFromMessage, opts ...rest.RequestOpt) (*discord.GuildThread, error)
	SendTyping(channelID snowflake.ID, opts ...rest.RequestOpt) error
	AddReaction(channelID snowflake.ID, messageID snowflake.ID, emoji string, opts ...rest.RequestOpt) error
	RemoveOwnReaction(channelID snowflake.ID, messageID snowflake.ID, emoji string, opts ...rest.RequestOpt) error
	DeleteMessage(channelID snowflake.ID, messageID snowflake.ID, opts ...rest.RequestOpt) error
	DeleteChannel(channelID snowflake.ID, opts ...rest.RequestOpt) error
}

var _ Discord = rest.Rest(nil)

// isUnknownChannel reports Discord's "Unknown Channel": the channel was
// deleted.
func isUnknownChannel(err error) bool {
	var re *rest.Error
	return errors.As(err, &re) && re.Code == rest.JSONErrorCodeUnknownChannel
}
