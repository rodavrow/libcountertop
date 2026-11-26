package detect

import (
	"github.com/ygelfand/libcountertop/pkg/inference/wake"
	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const GeminiGroup setting.Group = "Gemini"

type GeminiOptions struct {
	Read    func() bool
	Save    func(bool) error
	Changed func()
}

type GeminiSwitch struct {
	engine   *Engine
	options  GeminiOptions
	controls *setting.Controls[schema.Wake]
}

func NewGeminiSwitch(engine *Engine, options GeminiOptions) *GeminiSwitch {
	g := &GeminiSwitch{engine: engine, options: options}
	g.controls = &setting.Controls[schema.Wake]{
		Table: GeminiTable(),
		Read:  func() schema.Wake { return schema.Wake{Gemini: options.Read()} },
		Save:  g.save,
	}
	return g
}

func (g *GeminiSwitch) Controls() *setting.Controls[schema.Wake] { return g.controls }

func GeminiTable() *setting.Table[schema.Wake] {
	return setting.NewTable(
		stopDomain,
		[]setting.Group{GeminiGroup},
		[]setting.Setting[schema.Wake]{
			setting.Flag(
				setting.Setting[schema.Wake]{
					Name:  "gemini",
					Group: GeminiGroup,
					Icon:  "mdi:google-assistant",
				},
				func(w *schema.Wake) *bool { return &w.Gemini },
			),
		},
		setting.Messages{Text: say.T, Missing: say.Missing},
	)
}

func (g *GeminiSwitch) On() bool { return g.options.Read() }

func (g *GeminiSwitch) Set(on bool) error {
	if on == g.options.Read() {
		return nil
	}
	if err := g.options.Save(on); err != nil {
		return err
	}
	if !on {
		g.engine.ClearKind(wake.KindGemini)
	}
	g.options.Changed()
	g.controls.Publish()
	return nil
}

func (g *GeminiSwitch) save(row setting.Setting[schema.Wake], v string) error {
	at := schema.Wake{Gemini: g.options.Read()}
	if err := row.Write(&at, v); err != nil {
		return err
	}
	return g.Set(at.Gemini)
}
