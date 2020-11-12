package musicstreaming

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"

	"github.com/jonas747/discordgo"
	"github.com/jonas747/yagpdb/common"
	"github.com/jonas747/yagpdb/common/cplogs"
	"github.com/jonas747/yagpdb/common/featureflags"
	"github.com/jonas747/yagpdb/common/pubsub"
	"github.com/jonas747/yagpdb/web"
	"goji.io"
	"goji.io/pat"
)

type ConextKey int

const (
	ConextKeyConfig ConextKey = iota
)

var panelLogKey = cplogs.RegisterActionFormat(&cplogs.ActionFormat{Key: "musicstreaming_settings_updated", FormatString: "Updated music streaming settings"})

func (p *Plugin) InitWeb() {
	web.LoadHTMLTemplate("../../musicstreaming/assets/musicstreaming.html", "templates/plugins/musicstreaming.html")
	web.AddSidebarItem(web.SidebarCategoryFun, &web.SidebarItem{
		Name: "MusicStreaming",
		URL:  "musicstreaming",
		Icon: "fas fa-video",
	})

	musicCtreamingMux := goji.SubMux()
	web.CPMux.Handle(pat.New("/musicstreaming/*"), musicCtreamingMux)
	web.CPMux.Handle(pat.New("/musicstreaming"), musicCtreamingMux)

	// Alll handlers here require guild channels present
	musicCtreamingMux.Use(web.RequireBotMemberMW)
	musicCtreamingMux.Use(web.RequirePermMW(discordgo.PermissionManageRoles))
	musicCtreamingMux.Use(baseData)

	// Get just renders the template, so let the renderhandler do all the work
	musicCtreamingMux.Handle(pat.Get(""), web.RenderHandler(nil, "cp_musicstreaming"))
	musicCtreamingMux.Handle(pat.Get("/"), web.RenderHandler(nil, "cp_musicstreaming"))

	musicCtreamingMux.Handle(pat.Post(""), web.FormParserMW(web.RenderHandler(HandlePostStreaming, "cp_musicstreaming"), Config{}))
	musicCtreamingMux.Handle(pat.Post("/"), web.FormParserMW(web.RenderHandler(HandlePostStreaming, "cp_musicstreaming"), Config{}))
}

// Adds the current config to the context
func baseData(inner http.Handler) http.Handler {
	mw := func(w http.ResponseWriter, r *http.Request) {
		guild, tmpl := web.GetBaseCPContextData(r.Context())
		config, err := GetConfig(guild.ID)
		if web.CheckErr(tmpl, err, "Failed retrieving music streaming config :'(", web.CtxLogger(r.Context()).Error) {
			web.LogIgnoreErr(web.Templates.ExecuteTemplate(w, "cp_musicstreaming", tmpl))
			return
		}
		tmpl["MusicStreamingConfig"] = config
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ConextKeyConfig, config)))
	}

	return http.HandlerFunc(mw)
}

func HandlePostStreaming(w http.ResponseWriter, r *http.Request) interface{} {
	ctx := r.Context()
	guild, tmpl := web.GetBaseCPContextData(ctx)
	tmpl["VisibleURL"] = "/manage/" + discordgo.StrID(guild.ID) + "/musicstreaming/"

	ok := ctx.Value(common.ContextKeyFormOk).(bool)
	newConf := ctx.Value(common.ContextKeyParsedForm).(*Config)

	tmpl["MusicStreamingConfig"] = newConf

	if !ok {
		return tmpl
	}

	err := newConf.Save(guild.ID)
	if web.CheckErr(tmpl, err, "Failed saving config :'(", web.CtxLogger(ctx).Error) {
		return tmpl
	}

	err = featureflags.UpdatePluginFeatureFlags(guild.ID, &Plugin{})
	if err != nil {
		web.CtxLogger(ctx).WithError(err).Error("failed updating feature flags")
	}

	err = pubsub.Publish("update_musicstreaming", guild.ID, nil)
	if err != nil {
		web.CtxLogger(ctx).WithError(err).Error("Failed sending update music streaming event")
	}

	go cplogs.RetryAddEntry(web.NewLogEntryFromContext(r.Context(), panelLogKey))

	return tmpl.AddAlerts(web.SucessAlert("Saved settings"))
}

var _ web.PluginWithServerHomeWidget = (*Plugin)(nil)

func (p *Plugin) LoadServerHomeWidget(w http.ResponseWriter, r *http.Request) (web.TemplateData, error) {
	ag, templateData := web.GetBaseCPContextData(r.Context())

	templateData["WidgetTitle"] = "MusicStreaming"
	templateData["SettingsPath"] = "/musicstreaming"

	config, err := GetConfig(ag.ID)
	if err != nil {
		return templateData, err
	}

	format := `<ul>
	<li>Streaming status: %s</li>
	<li>Streaming role: <code>%s</code>%s</li>
	<li>Streaming message: <code>#%s</code>%s</li>
</ul>`

	status := web.EnabledDisabledSpanStatus(config.Enabled)

	if config.Enabled {
		templateData["WidgetEnabled"] = true
	} else {
		templateData["WidgetDisabled"] = true
	}

	roleStr := "none / unknown"
	indicatorRole := ""
	if role := ag.Role(config.GiveRole); role != nil {
		roleStr = html.EscapeString(role.Name)
		indicatorRole = web.Indicator(true)
	} else {
		indicatorRole = web.Indicator(false)
	}

	indicatorMessage := ""
	channelStr := "none / unknown"

	if channel := ag.Channel(config.AnnounceChannel); channel != nil {
		indicatorMessage = web.Indicator(true)
		channelStr = html.EscapeString(channel.Name)
	} else {
		indicatorMessage = web.Indicator(false)
	}

	templateData["WidgetBody"] = template.HTML(fmt.Sprintf(format, status, roleStr, indicatorRole, channelStr, indicatorMessage))

	return templateData, nil
}
