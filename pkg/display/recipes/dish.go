package recipes

import (
	"strconv"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/mealie"
	"github.com/ygelfand/libcountertop/pkg/say"
)

func (a *App) dishView(w *gogui.Window) gogui.View {
	a.mu.Lock()
	slug := a.slug
	a.mu.Unlock()
	r, err, done := a.o.Kitchen.Recipe(slug)
	switch {
	case !done:
		return note(say.T("home.loading"))
	case err != nil:
		return note(a.o.Kitchen.Status().Says())
	}
	vw, vh := w.WindowSize()
	width := widgets.PageWidth(w)
	scale := a.scale(r)
	about := a.about(w, r, scale, width)
	lists := []gogui.View{a.ingredients(r, scale), a.steps(r)}
	if vw < vh {
		return a.scroller("recipes-dish", append([]gogui.View{about}, lists...))
	}
	side := width * 0.4
	return gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingLarge,
		Content: []gogui.View{
			gogui.Column(gogui.ContainerCfg{
				ID: "recipes-dish-about", Width: side, Sizing: gogui.FixedFill,
				Padding:    gogui.NewPadding(0, style.Reach()*0.4, 0, 0),
				Scrollable: true, OnGesture: a.o.UI.HoldStill,
				Content: []gogui.View{about},
			}),
			a.scroller("recipes-dish-lists", lists),
		},
	})
}

func (a *App) dishHead(w *gogui.Window) gogui.View {
	a.mu.Lock()
	slug := a.slug
	a.mu.Unlock()
	r, err, done := a.o.Kitchen.Recipe(slug)
	if !done || err != nil {
		return a.head(w, nil)
	}
	favorite := a.o.Kitchen.Favorite(r.ID)
	heart := "mdi:heart-outline"
	if favorite {
		heart = "mdi:heart"
	}
	return a.head(w, []gogui.View{
		a.chip("recipes-favorite", heart, "", favorite,
			func() { a.o.Kitchen.SetFavorite(r.Summary, !favorite) }),
		a.chip("recipes-cook", "mdi:chef-hat", say.T("recipes.cook"), true, func() { a.start(r) }),
	})
}

func (a *App) cookHead(w *gogui.Window) gogui.View { return a.head(w, nil) }

func (a *App) head(w *gogui.Window, actions []gogui.View) gogui.View {
	st := gogui.CurrentTheme().TextStyleDisplay
	reach := style.Reach()
	room := widgets.PageWidth(w) - reach*1.5
	if len(actions) > 0 {
		label := gogui.CurrentTheme().Cfg.TextStyleDef
		room -= w.TextWidth(say.T("recipes.cook"), label) + reach*2.6
	}
	content := []gogui.View{
		gogui.Label(widgets.Ellipsize(w, a.title(), st, room), st),
		gogui.Row(gogui.ContainerCfg{Sizing: gogui.FillFit, Padding: gogui.NoPadding}),
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		VAlign: gogui.VAlignMiddle, Content: append(content, actions...),
	})
}

func (a *App) about(w *gogui.Window, r mealie.Recipe, scale float64, width float32) gogui.View {
	vw, vh := w.WindowSize()
	side := width * 0.4
	if vw < vh {
		side = width - style.Reach()*0.4
	}
	title := gogui.CurrentTheme().TextStyleDisplay
	views := []gogui.View{a.picture(r.Summary, side, side*0.75), wrap(r.Name, title)}
	if d := mealie.Plain(r.Description); d != "" {
		views = append(views, wrap(d, widgets.Secondary()))
	}
	var facts []gogui.View
	if d := r.Total(); d > 0 {
		facts = append(facts, fact("mdi:clock-outline", duration(d)))
	}
	if r.PrepSecs != nil && *r.PrepSecs > 0 {
		facts = append(facts, fact("mdi:knife", say.F("recipes.prep",
			map[string]any{"Time": duration(time.Duration(*r.PrepSecs) * time.Second)})))
	}
	if len(facts) > 0 {
		views = append(views, gogui.Row(gogui.ContainerCfg{
			Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingLarge,
			Content: facts,
		}))
	}
	views = append(views, a.servingsRow(r, scale))
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: views,
	})
}

func (a *App) servingsRow(r mealie.Recipe, scale float64) gogui.View {
	if r.Servings <= 0 {
		if r.Yield == "" {
			return gogui.Row(gogui.ContainerCfg{Sizing: gogui.FillFit, Padding: gogui.NoPadding})
		}
		return fact("mdi:silverware-fork-knife", r.Yield)
	}
	servings := r.Servings * scale
	label := say.F("recipes.servings", map[string]any{"N": trim(servings)})
	return gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{
			a.chip("recipes-fewer", "mdi:minus", "", false, func() { a.serve(r, servings-1) }),
			gogui.Label(label, gogui.CurrentTheme().Cfg.TextStyleDef),
			a.chip("recipes-more-servings", "mdi:plus", "", false,
				func() { a.serve(r, servings+1) }),
		},
	})
}

func (a *App) scale(r mealie.Recipe) float64 {
	if s, ok := a.o.Kitchen.Session(); ok && s.Recipe.Slug == r.Slug {
		return s.Scale()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n, ok := a.servings[r.Slug]; ok && r.Servings > 0 {
		return n / r.Servings
	}
	return 1
}

func (a *App) serve(r mealie.Recipe, n float64) {
	if n < 1 {
		return
	}
	a.mu.Lock()
	a.servings[r.Slug] = n
	a.mu.Unlock()
	if a.o.Kitchen.Cooking(r.Slug) {
		a.o.Kitchen.Serve(n)
	}
	a.redraw()
}

func (a *App) start(r mealie.Recipe) {
	if !a.o.Kitchen.Cooking(r.Slug) {
		a.o.Kitchen.Cook(r)
		a.mu.Lock()
		n, ok := a.servings[r.Slug]
		a.mu.Unlock()
		if ok {
			a.o.Kitchen.Serve(n)
		}
	}
	a.mu.Lock()
	a.slug = r.Slug
	a.mu.Unlock()
	a.o.Shell.Push(a.cook)
}

func (a *App) ingredients(r mealie.Recipe, scale float64) gogui.View {
	views := []gogui.View{heading(say.T("recipes.ingredients"))}
	a.mu.Lock()
	ticked := a.ticked[r.Slug]
	a.mu.Unlock()
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	for n, in := range r.Ingredients {
		if in.Title != "" {
			views = append(views, gogui.Label(in.Title, widgets.Secondary()))
		}
		text := in.Scaled(scale)
		if text == "" {
			continue
		}
		done := ticked[n]
		st := body
		glyph := "mdi:checkbox-blank-circle-outline"
		if done {
			st = widgets.Secondary()
			glyph = "mdi:check-circle"
		}
		views = append(views, a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
			ID:      "recipes-ingredient-" + strconv.Itoa(n),
			Sizing:  gogui.FillFit,
			Padding: gogui.PaddingSmall,
			Spacing: gogui.SpacingMedium,
			Radius:  gogui.RadiusMedium,
			VAlign:  gogui.VAlignMiddle,
			Content: []gogui.View{icon(glyph, body.Size*1.1, st.Color), wrap(text, st)},
		}, func(e gogui.EventCtx) {
			a.tick(r.Slug, n)
			e.Consume()
		}))
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Content: views,
	})
}

func (a *App) tick(slug string, n int) {
	a.mu.Lock()
	if a.ticked[slug] == nil {
		a.ticked[slug] = map[int]bool{}
	}
	a.ticked[slug][n] = !a.ticked[slug][n]
	a.mu.Unlock()
	a.redraw()
}

func (a *App) steps(r mealie.Recipe) gogui.View {
	views := []gogui.View{heading(say.T("recipes.steps"))}
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	number := body
	number.Color = gogui.CurrentTheme().Cfg.ColorAccent
	for n, step := range r.Steps {
		if step.Title != "" {
			views = append(views, gogui.Label(step.Title, widgets.Secondary()))
		}
		views = append(views, a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
			ID:      "recipes-step-" + strconv.Itoa(n),
			Sizing:  gogui.FillFit,
			Padding: gogui.PaddingSmall,
			Spacing: gogui.SpacingMedium,
			Radius:  gogui.RadiusMedium,
			Content: []gogui.View{
				gogui.Label(strconv.Itoa(n+1), number),
				wrap(mealie.Plain(step.Text), body),
			},
		}, func(e gogui.EventCtx) {
			a.start(r)
			a.o.Kitchen.Goto(n)
			e.Consume()
		}))
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Content: views,
	})
}

func heading(text string) gogui.View {
	st := gogui.CurrentTheme().Cfg.TextStyleDef
	st.Size *= 1.25
	return gogui.Label(text, st)
}

func fact(glyph, text string) gogui.View {
	st := widgets.Secondary()
	return gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FitFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		VAlign:  gogui.VAlignMiddle,
		Content: []gogui.View{icon(glyph, st.Size*1.2, st.Color), gogui.Label(text, st)},
	})
}

func icon(glyph string, size float32, color gogui.Color) gogui.View {
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size, st.Color = icons.Family, size, color
	return gogui.Label(icons.Glyph(icons.Named(glyph)), st)
}

func trim(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }
