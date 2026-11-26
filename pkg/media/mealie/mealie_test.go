package mealie

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

func qty(f float64) *float64 { return &f }

func TestScaledAmountsReadLikeMealie(t *testing.T) {
	cup := &Unit{Name: "cup", PluralName: "cups"}
	flour := &Food{Name: "flour"}
	egg := &Food{Name: "egg", PluralName: "eggs"}
	grams := &Unit{Name: "gram", Abbreviation: "g", UseAbbrevation: true, Fraction: new(bool)}
	for _, c := range []struct {
		in   Ingredient
		by   float64
		want string
	}{
		{Ingredient{Quantity: qty(1), Unit: cup, Food: flour, Display: "1 cup flour"}, 1, "1 cup flour"},
		{Ingredient{Quantity: qty(1), Unit: cup, Food: flour}, 1.5, "1½ cups flour"},
		{Ingredient{Quantity: qty(0.75), Unit: cup, Food: flour}, 0.5, "⅜ cup flour"},
		{Ingredient{Quantity: qty(2), Food: egg, Note: "beaten"}, 2, "4 eggs beaten"},
		{Ingredient{Quantity: qty(250), Unit: grams, Food: flour}, 0.3, "75 g flour"},
		{Ingredient{Quantity: qty(1), Unit: cup, Food: flour}, 1.0 / 3, "⅓ cup flour"},
		{Ingredient{Note: "salt to taste", Display: "salt to taste"}, 2, "salt to taste"},
		{
			Ingredient{Quantity: qty(1.5), Unit: &Unit{Name: "quart"}, Food: &Food{Name: "broth"}, Display: "1 ¹/₂ quart broth"},
			1,
			"1½ quart broth",
		},
		{Ingredient{Quantity: qty(2), Note: "2 cloves garlic", Display: "2 2 cloves garlic"}, 1, "2 2 cloves garlic"},
	} {
		if got := c.in.Scaled(c.by); got != c.want {
			t.Errorf("%+v ×%v read %q, want %q", c.in, c.by, got, c.want)
		}
	}
}

func TestAStepKnowsTheIngredientsItUses(t *testing.T) {
	s := Session{Recipe: Recipe{
		Ingredients: []Ingredient{
			{Display: "pasta", Reference: "a"},
			{Display: "salt", Reference: "b"},
			{Display: "basil", Reference: "c"},
		},
		Steps: []Step{
			{Text: "Boil", Ingredients: []Reference{{ID: "a"}, {ID: "b"}}},
			{Text: "Serve"},
		},
	}}
	if got := s.Uses(0); len(got) != 2 || got[0].Display != "pasta" || got[1].Display != "salt" {
		t.Errorf("step 1 uses %+v, want pasta and salt", got)
	}
	if got := s.Uses(1); len(got) != 0 {
		t.Errorf("step 2 uses %+v, want nothing", got)
	}
}

func TestPlainDropsMarkdown(t *testing.T) {
	if got := Plain("Add **2 cups** of [stock](http://x) and\n\nstir `well`"); got !=
		"Add 2 cups of stock and stir well" {
		t.Errorf("read %q", got)
	}
}

type server struct {
	mu       sync.Mutex
	favorite map[string]bool
	asked    []string
	failing  int
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.asked = append(s.asked, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.URL.Path == "/api/users/self":
		reply(map[string]any{"id": "u1", "username": "cook"})
	case r.URL.Path == "/api/users/self/favorites":
		s.mu.Lock()
		var ratings []map[string]any
		for id, on := range s.favorite {
			ratings = append(ratings, map[string]any{"recipeId": id, "isFavorite": on})
		}
		s.mu.Unlock()
		reply(map[string]any{"ratings": ratings})
	case strings.HasPrefix(r.URL.Path, "/api/users/u1/favorites/"):
		s.mu.Lock()
		s.favorite["r-"+strings.TrimPrefix(r.URL.Path, "/api/users/u1/favorites/")] =
			r.Method == http.MethodPost
		s.mu.Unlock()
	case r.URL.Path == "/api/recipes":
		items := []map[string]any{
			{"id": "r-pasta", "slug": "pasta", "name": "Pasta al pomodoro"},
			{"id": "r-soup", "slug": "soup", "name": "Tomato soup"},
		}
		if f := r.URL.Query().Get("queryFilter"); f != "" {
			items = items[:1]
		}
		reply(map[string]any{"page": 1, "total_pages": 1, "items": items})
	case r.URL.Path == "/api/recipes/pasta" && s.fail():
		w.WriteHeader(http.StatusBadGateway)
	case r.URL.Path == "/api/recipes/pasta":
		reply(map[string]any{
			"id": "r-pasta", "slug": "pasta", "name": "Pasta al pomodoro", "recipeServings": 2,
			"recipeIngredient": []map[string]any{
				{"quantity": 200, "unit": map[string]any{"name": "gram", "abbreviation": "g",
					"useAbbreviation": true, "fraction": false},
					"food": map[string]any{"name": "spaghetti"}, "referenceId": "i1"},
				{"quantity": 0, "note": "salt", "display": "salt", "referenceId": "i2"},
			},
			"recipeInstructions": []map[string]any{
				{"text": "Boil the **spaghetti**.", "ingredientReferences": []map[string]any{
					{"referenceId": "i1"}}, "durationSeconds": 600},
				{"text": "Add sauce."},
			},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *server) fail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing == 0 {
		return false
	}
	s.failing--
	return true
}

type timers struct {
	mu    sync.Mutex
	asked []time.Duration
}

func (t *timers) Count(_, _ string, d time.Duration) {
	t.mu.Lock()
	t.asked = append(t.asked, d)
	t.mu.Unlock()
}

func rig(t *testing.T, token string) (*Kitchen, *server, *timers) {
	t.Helper()
	srv := &server{favorite: map[string]bool{"r-pasta": true}}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	section := &memory{at: schema.Mealie{Server: ts.URL, Token: token}}
	clock := &timers{}
	return New(Options{Settings: section, Timers: clock}), srv, clock
}

type memory struct {
	mu sync.Mutex
	at schema.Mealie
}

func (m *memory) Read() schema.Mealie {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.at
}

func (m *memory) Update(change func(*schema.Mealie)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(&m.at)
	return nil
}

func eventually(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTheKitchenConnectsAndListsFavorites(t *testing.T) {
	k, _, _ := rig(t, "secret")
	k.Check()
	eventually(t, func() bool { return k.Status() == Ready })
	if !k.Favorite("r-pasta") || k.Favorite("r-soup") {
		t.Error("favorites not read from Mealie")
	}
	k.List("", true)
	eventually(t, func() bool { return k.List("", true).Loaded })
	if got := k.List("", true).Items; len(got) != 1 || got[0].Slug != "pasta" {
		t.Errorf("favorites shelf %+v, want only pasta", got)
	}
}

func TestAFailedRecipeLoadsOnTheNextOpen(t *testing.T) {
	k, srv, _ := rig(t, "secret")
	srv.failing = 1
	eventually(t, func() bool { _, _, done := k.Recipe("pasta"); return done })
	if _, err, _ := k.Recipe("pasta"); err == nil {
		t.Fatal("the first load did not fail")
	}
	k.Refresh()
	eventually(t, func() bool { _, err, done := k.Recipe("pasta"); return done && err == nil })
	if r, _, _ := k.Recipe("pasta"); r.Name != "Pasta al pomodoro" {
		t.Errorf("reopened recipe %q, want Pasta al pomodoro", r.Name)
	}
}

func TestARefusedTokenIsReported(t *testing.T) {
	k, _, _ := rig(t, "wrong")
	k.Check()
	eventually(t, func() bool { return k.Status() == Refused })
}

func TestFavoritingIsSentToMealie(t *testing.T) {
	k, srv, _ := rig(t, "secret")
	k.Check()
	eventually(t, func() bool { return k.Status() == Ready })
	k.SetFavorite(Summary{ID: "r-soup", Slug: "soup"}, true)
	if !k.Favorite("r-soup") {
		t.Error("favorite not shown at once")
	}
	eventually(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return srv.favorite["r-soup"]
	})
}

func call(op string, args map[string]string) esphome.Call {
	all := map[string]any{"operation": op}
	for k, v := range args {
		all[k] = v
	}
	c, err := esphome.NewCall(all)
	if err != nil {
		panic(err)
	}
	return c
}

func TestCookingByVoiceWalksTheSteps(t *testing.T) {
	defer say.UseVoice("")
	say.UseVoice("en")
	k, _, clock := rig(t, "secret")
	var shown []Show
	k.Asked.Listen(func(s Show) { shown = append(shown, s) })
	run := k.VoiceAction().Run

	reply, err := run(call("cook", map[string]string{"recipe": "pasta"}))
	if err != nil || !strings.Contains(reply, "Pasta al pomodoro") ||
		!strings.Contains(reply, "Boil the spaghetti.") {
		t.Fatalf("cook replied %q, %v", reply, err)
	}
	if len(shown) != 1 || shown[0].Kind != ShowCook || shown[0].Slug != "pasta" {
		t.Errorf("asked the screen for %+v, want cook mode for pasta", shown)
	}
	if reply, _ := run(call("amount", map[string]string{"ingredient": "spaghetti"})); reply !=
		"You need 200 g spaghetti" {
		t.Errorf("amount replied %q", reply)
	}
	if reply, _ := run(call("timer", nil)); !strings.Contains(reply, "10 minutes") {
		t.Errorf("timer replied %q", reply)
	}
	if len(clock.asked) != 1 || clock.asked[0] != 10*time.Minute {
		t.Errorf("timers asked for %v, want one of ten minutes", clock.asked)
	}
	if reply, _ := run(call("next", nil)); !strings.Contains(reply, "Step 2 of 2") {
		t.Errorf("next replied %q", reply)
	}
	if reply, _ := run(call("next", nil)); reply != say.Speak("actions.recipes.last") {
		t.Errorf("next past the end replied %q", reply)
	}
	if reply, _ := run(call("previous", nil)); !strings.Contains(reply, "Step 1 of 2") {
		t.Errorf("previous replied %q", reply)
	}
	k.Done()
	if reply, _ := run(call("next", nil)); reply != say.Speak("actions.recipes.closed") {
		t.Errorf("next with nothing open replied %q", reply)
	}
}

func TestTheVoiceActionHasATriggerPerSentence(t *testing.T) {
	ids, err := say.Ids()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, id := range ids {
		if strings.HasPrefix(id, "actions.recipes.sentence.") {
			want[id] = true
		}
	}
	for _, tr := range (&Kitchen{}).VoiceAction().Triggers {
		if !want[tr.Key] {
			t.Errorf("trigger %s has no sentence", tr.Key)
		}
		delete(want, tr.Key)
	}
	for id := range want {
		t.Errorf("sentence %s has no trigger", id)
	}
}
