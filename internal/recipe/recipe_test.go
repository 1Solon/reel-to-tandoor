package recipe

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	source  = "https://www.instagram.com/reel/DfXD-34Bf40/"
	caption = "Comment “yes” for my new family cookbook! \n\n#wellnesstips #health #wellnessjourney"
)

// lentilBread is the real /api/ai-import/ response for the test reel (GLM-5.3 Flash, 2026-11-06).
func lentilBread(t *testing.T) AIRecipe {
	t.Helper()
	data, err := os.ReadFile("testdata/ai_import_lentil_bread.json")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Recipe AIRecipe `json:"recipe"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	return body.Recipe
}

func ing(food, unit string, amount float64) AIIngredient {
	a := amount
	i := AIIngredient{Amount: &a, Food: &Named{food}, OriginalText: food}
	if unit != "" {
		i.Unit = &Named{unit}
	}
	return i
}

func foodsByStep(p Payload) [][]string {
	out := [][]string{}
	for _, s := range p.Steps {
		names := []string{}
		for _, i := range s.Ingredients {
			names = append(names, i.Food.Name)
		}
		out = append(out, names)
	}
	return out
}

func TestCleanCaption(t *testing.T) {
	cases := map[string]string{
		caption:                              "Comment “yes” for my new family cookbook!",
		"":                                   "",
		"Lentil bread #vegan\n\n#glutenfree": "Lentil bread",
		"Crêpes #café-au-lait done":          "Crêpes  done",
		"Use 1 cup water (not #2 tap)":       "Use 1 cup water (not  tap)",
	}
	for in, want := range cases {
		if got := CleanCaption(in); got != want {
			t.Errorf("CleanCaption(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeAIText(t *testing.T) {
	got, err := ComposeAIText(caption, "Soak two cups of red lentils overnight.")
	want := "Caption:\nComment “yes” for my new family cookbook!\n\n" +
		"Transcript of the video's narration:\nSoak two cups of red lentils overnight."
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, _ := ComposeAIText("", "Soak lentils."); got != "Transcript of the video's narration:\nSoak lentils." {
		t.Errorf("transcript only: %q", got)
	}
	if got, _ := ComposeAIText("2 cups lentils, bake 45 min", "  "); got != "Caption:\n2 cups lentils, bake 45 min" {
		t.Errorf("caption only: %q", got)
	}
	var noRecipe *NoRecipeError
	if _, err := ComposeAIText("#food #yum", ""); !errors.As(err, &noRecipe) {
		t.Errorf("empty reel error = %v", err)
	}
}

func TestFallbackName(t *testing.T) {
	cases := map[string]string{
		"Easy lentil bread 🍞 #vegan":                                          "Easy lentil bread 🍞",
		"Comment 'yes' for my cookbook!\nHigh-protein lentil bread. So good!": "High-protein lentil bread",
		"@someone\nCrispy chickpea wraps":                                     "Crispy chickpea wraps",
		caption:                                                               "Recipe from cameronjames.wellness",
		"":                                                                    "Recipe from cameronjames.wellness",
	}
	for in, want := range cases {
		if got := FallbackName(in, "cameronjames.wellness"); got != want {
			t.Errorf("FallbackName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := FallbackName("Follow for more", ""); got != "Recipe from a reel" {
		t.Errorf("no uploader: %q", got)
	}
}

func TestPayloadFromRealAIResponse(t *testing.T) {
	p, err := BuildPayload(lentilBread(t), source, caption, "cameronjames.wellness")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Flourless Lentil Bread" || p.SourceURL != source || !p.Internal {
		t.Errorf("unexpected header fields: %+v", p)
	}
	if p.Servings != 1 { // AI returned null
		t.Errorf("servings = %d", p.Servings)
	}
	if p.ServingsText != "" { // AI returned the string "None"
		t.Errorf("servings_text = %q", p.ServingsText)
	}
	if !reflect.DeepEqual(p.Keywords, []Named{{"✨ AI"}}) {
		t.Errorf("keywords = %v", p.Keywords)
	}
	if p.Steps[0].Instruction != "Soak two cups of red lentils overnight." {
		t.Errorf("step 1 = %q", p.Steps[0].Instruction)
	}
	want := [][]string{{"red lentils"}, {"fresh water", "baking powder", "salt"}, {"seeds"}, {}, {}}
	if got := foodsByStep(p); !reflect.DeepEqual(got, want) {
		t.Errorf("ingredients by step = %v, want %v", got, want)
	}

	body, _ := json.Marshal(p)
	if strings.Contains(string(body), `"order"`) {
		t.Errorf("payload sends order: %s", body)
	}
	if !strings.Contains(string(body), `"unit":null`) {
		t.Errorf("unitless ingredient should send unit null: %s", body)
	}
}

func TestPayloadKeepsSetOrder(t *testing.T) {
	two := 2
	i := ing("flour", "", 1)
	i.Order = &two
	p, _ := BuildPayload(AIRecipe{Name: "X", Steps: []AIStep{{Instruction: "Mix", Ingredients: []AIIngredient{i}}}}, source, "", "")
	if got := p.Steps[0].Ingredients[0].Order; got == nil || *got != 2 {
		t.Errorf("order = %v", got)
	}
}

func TestPayloadFallsBackWhenNameBlank(t *testing.T) {
	r := lentilBread(t)
	r.Name = "  "
	p, _ := BuildPayload(r, source, "Lentil bread, no flour! #vegan", "c")
	if p.Name != "Lentil bread, no flour" {
		t.Errorf("name = %q", p.Name)
	}
}

func TestPayloadSplitsFoodNote(t *testing.T) {
	i := ing("seeds (for topping)", "", 0)
	i.Note = "optional"
	p, _ := BuildPayload(AIRecipe{Name: "Bread", Steps: []AIStep{{Instruction: "Top", Ingredients: []AIIngredient{i}}}}, source, "", "")
	got := p.Steps[0].Ingredients[0]
	if got.Food.Name != "seeds" || got.Note != "for topping, optional" {
		t.Errorf("got %+v", got)
	}
}

func TestPayloadSplitsSingleMultilineStep(t *testing.T) {
	r := AIRecipe{Name: "Bread", Steps: []AIStep{{
		Instruction: "Soak the lentils.\n\nBlend with water.\nBake.",
		Ingredients: []AIIngredient{ing("lentils", "cups", 2), ing("water", "cup", 1)},
	}}}
	p, _ := BuildPayload(r, source, "", "")
	var instructions []string
	for _, s := range p.Steps {
		instructions = append(instructions, s.Instruction)
	}
	if !reflect.DeepEqual(instructions, []string{"Soak the lentils.", "Blend with water.", "Bake."}) {
		t.Errorf("instructions = %v", instructions)
	}
	if got := foodsByStep(p); !reflect.DeepEqual(got, [][]string{{"lentils"}, {"water"}, {}}) {
		t.Errorf("ingredients by step = %v", got)
	}
}

func TestPayloadHandlesDuplicateIngredients(t *testing.T) {
	r := AIRecipe{Name: "Two sauces", Steps: []AIStep{
		{Instruction: "Gather", Ingredients: []AIIngredient{ing("salt", "", 1), ing("salt", "", 1)}},
		{Instruction: "Add salt"},
	}}
	p, _ := BuildPayload(r, source, "", "")
	if len(p.Steps[0].Ingredients) != 0 || len(p.Steps[1].Ingredients) != 2 {
		t.Errorf("ingredients by step = %v", foodsByStep(p))
	}
}

func TestPayloadDropsBlankIngredientsAndSteps(t *testing.T) {
	r := AIRecipe{Name: "Bread", Steps: []AIStep{
		{},
		{Instruction: "Bake", Ingredients: []AIIngredient{ing("", "", 1), {Food: nil}, ing("flour", "", 1)}},
	}}
	p, _ := BuildPayload(r, source, "", "")
	if got := foodsByStep(p); !reflect.DeepEqual(got, [][]string{{"flour"}}) {
		t.Errorf("ingredients by step = %v", got)
	}
}

func TestPayloadKeepsLongServingsTextAndTruncates(t *testing.T) {
	r := lentilBread(t)
	eight := 8.0
	r.Servings, r.ServingsText = &eight, "8 slices"
	r.Name, r.Description = strings.Repeat("n", 300), strings.Repeat("é", 900)
	p, _ := BuildPayload(r, source, "", "")
	if p.Servings != 8 || p.ServingsText != "8 slices" {
		t.Errorf("servings = %d %q", p.Servings, p.ServingsText)
	}
	if len([]rune(p.Name)) != 128 || len([]rune(p.Description)) != 512 {
		t.Errorf("lengths = %d %d", len([]rune(p.Name)), len([]rune(p.Description)))
	}
}

func TestPayloadRefusesEmptyRecipe(t *testing.T) {
	for _, r := range []AIRecipe{
		{},
		{Name: "Lentil Bread"},
		{Steps: []AIStep{{Instruction: " "}}},
	} {
		_, err := BuildPayload(r, source, "", "")
		var noRecipe *NoRecipeError
		if !errors.As(err, &noRecipe) || err.Error() != "No recipe found in this reel." {
			t.Errorf("BuildPayload(%+v) error = %v", r, err)
		}
	}
}
