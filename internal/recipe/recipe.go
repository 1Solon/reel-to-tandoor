// Package recipe turns a reel's caption and transcript into the text for Tandoor's AI import,
// and turns the AI's answer into a body that POST /api/recipe/ accepts.
//
// The payload rules follow Kitshn's TandoorRecipeFromSource.create():
// https://github.com/kitshn-app/kitshn/blob/main/shared/src/commonMain/kotlin/de/kitshn/api/tandoor/model/recipe/TandoorRecipeFromSource.kt
package recipe

import (
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Tandoor model field limits (cookbook/models.py).
const (
	nameMax        = 128
	descriptionMax = 512
)

var (
	hashtagRE     = regexp.MustCompile(`(^|[^\p{L}\p{N}_])#[\p{L}\p{N}_-]+`)
	mentionOnlyRE = regexp.MustCompile(`^(@[\p{L}\p{N}_.]+\s*)+$`)
	// Lines that are calls to action rather than a dish name.
	ctaRE = regexp.MustCompile(`(?i)\b(comment|follow|link in (my )?bio|save (this|it)|share (this|it)|dm\b|subscribe|giveaway|tag a)`)
	// The end of the first sentence: punctuation followed by whitespace.
	sentenceEndRE = regexp.MustCompile(`[.!?]\s`)
)

// NoRecipeError means the reel had no recipe in its caption or narration.
// Its message is safe to show to the user.
type NoRecipeError struct{ msg string }

func (e *NoRecipeError) Error() string { return e.msg }

// CleanCaption drops hashtags and blank lines; they add noise to the AI prompt and never hold
// the recipe.
func CleanCaption(caption string) string {
	var lines []string
	for _, line := range strings.Split(caption, "\n") {
		line = strings.TrimSpace(hashtagRE.ReplaceAllString(line, "$1"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// ComposeAIText builds the text field for /api/ai-import/. It returns a NoRecipeError when
// there's nothing to send, so no AI call is spent on it.
//
// There's no uploader line: GLM turns the creator's handle into a keyword.
func ComposeAIText(caption, transcript string) (string, error) {
	caption = CleanCaption(caption)
	transcript = strings.TrimSpace(transcript)
	if caption == "" && transcript == "" {
		return "", &NoRecipeError{"No recipe found in this reel (it has no caption and no narration)."}
	}
	var parts []string
	if caption != "" {
		parts = append(parts, "Caption:\n"+caption)
	}
	if transcript != "" {
		parts = append(parts, "Transcript of the video's narration:\n"+transcript)
	}
	return strings.Join(parts, "\n\n"), nil
}

// FallbackName is used when the AI leaves the name blank (GLM does this for about half of all
// captions). It takes the caption's first line that isn't a call to action, else
// "Recipe from <uploader>".
func FallbackName(caption, uploader string) string {
	for _, line := range strings.Split(CleanCaption(caption), "\n") {
		line = strings.Trim(line, " -–—:|•*")
		if line == "" || mentionOnlyRE.MatchString(line) || ctaRE.MatchString(line) {
			continue
		}
		// First sentence only; captions often run on into the story behind the dish.
		if loc := sentenceEndRE.FindStringIndex(line); loc != nil {
			line = line[:loc[0]+1]
		}
		line = strings.TrimRight(line, ".!? ")
		if n := utf8.RuneCountInString(line); n >= 3 && n <= 80 {
			return line
		}
	}
	if uploader != "" {
		return "Recipe from " + uploader
	}
	return "Recipe from a reel"
}

// Named is a {"name": ...} object, used for foods, units and keywords.
type Named struct {
	Name string `json:"name"`
}

// AIIngredient is an ingredient as /api/ai-import/ returns it.
type AIIngredient struct {
	Amount       *float64 `json:"amount"`
	Food         *Named   `json:"food"`
	Unit         *Named   `json:"unit"`
	Note         string   `json:"note"`
	Order        *int     `json:"order"`
	OriginalText string   `json:"original_text"`
}

// AIStep is a step as /api/ai-import/ returns it.
type AIStep struct {
	Instruction          string         `json:"instruction"`
	Ingredients          []AIIngredient `json:"ingredients"`
	ShowIngredientsTable *bool          `json:"show_ingredients_table"`
}

// AIRecipe is the unsaved, RecipeFromSource-shaped recipe from /api/ai-import/.
type AIRecipe struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Servings     *float64 `json:"servings"`
	ServingsText string   `json:"servings_text"`
	WorkingTime  *float64 `json:"working_time"`
	WaitingTime  *float64 `json:"waiting_time"`
	Keywords     []Named  `json:"keywords"`
	Steps        []AIStep `json:"steps"`
}

// Ingredient is an ingredient in a POST /api/recipe/ body.
type Ingredient struct {
	Food         Named   `json:"food"`
	Unit         *Named  `json:"unit"`
	Amount       float64 `json:"amount"`
	Note         string  `json:"note"`
	OriginalText string  `json:"original_text"`
	// order: null makes the API reject the recipe, so it's only sent when it's set.
	Order *int `json:"order,omitempty"`
}

// Step is a step in a POST /api/recipe/ body.
type Step struct {
	Instruction          string       `json:"instruction"`
	Ingredients          []Ingredient `json:"ingredients"`
	ShowIngredientsTable bool         `json:"show_ingredients_table"`
}

// Payload is a POST /api/recipe/ body.
type Payload struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Servings     int     `json:"servings"`
	ServingsText string  `json:"servings_text"`
	WorkingTime  int     `json:"working_time"`
	WaitingTime  int     `json:"waiting_time"`
	SourceURL    string  `json:"source_url"`
	Internal     bool    `json:"internal"`
	Keywords     []Named `json:"keywords"`
	Steps        []Step  `json:"steps"`
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func intOr(v *float64, fallback int) int {
	if v == nil || *v == 0 {
		return fallback
	}
	return int(math.Round(*v))
}

func convertIngredient(raw AIIngredient) (Ingredient, bool) {
	food := ""
	if raw.Food != nil {
		food = strings.TrimSpace(raw.Food.Name)
	}
	if food == "" {
		return Ingredient{}, false
	}
	note := strings.TrimSpace(raw.Note)

	// "seeds (for topping)" -> food "seeds", note "for topping". Kitshn does the same so foods
	// don't get created once per note.
	if before, after, found := strings.Cut(food, " ("); found {
		food = before
		extra := strings.TrimSpace(strings.TrimRight(after, ")"))
		switch {
		case extra != "" && note != "":
			note = extra + ", " + note
		case extra != "":
			note = extra
		}
	}

	ing := Ingredient{
		Food:         Named{truncate(food, nameMax)},
		Note:         note,
		OriginalText: raw.OriginalText,
		Order:        raw.Order,
	}
	if raw.Amount != nil {
		ing.Amount = *raw.Amount
	}
	if raw.Unit != nil {
		if unit := strings.TrimSpace(raw.Unit.Name); unit != "" {
			ing.Unit = &Named{truncate(unit, nameMax)}
		}
	}
	return ing, true
}

// splitSingleStep splits everything into one step per line if it all landed in one step, like
// Kitshn.
func splitSingleStep(steps []Step) []Step {
	if len(steps) != 1 {
		return steps
	}
	var lines []string
	for _, line := range strings.Split(steps[0].Instruction, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) <= 1 {
		return steps
	}
	first := steps[0]
	first.Instruction = lines[0]
	out := []Step{first}
	for _, line := range lines[1:] {
		out = append(out, Step{Instruction: line, Ingredients: []Ingredient{}, ShowIngredientsTable: true})
	}
	return out
}

// assignIngredients moves each ingredient to the first later step that mentions it; the rest go
// on step 1. The AI usually lists every ingredient on step 1. Same heuristic as Kitshn's
// autoSortIngredients.
func assignIngredients(steps []Step) []Step {
	if len(steps) <= 1 {
		return steps
	}
	var remaining []Ingredient
	for _, step := range steps {
		remaining = append(remaining, step.Ingredients...)
	}
	out := make([]Step, len(steps))
	for i, step := range steps {
		step.Ingredients = []Ingredient{}
		out[i] = step
	}
	for i := 1; i < len(out); i++ {
		text := strings.ToLower(out[i].Instruction)
		var left []Ingredient
		for _, ing := range remaining {
			if strings.Contains(text, strings.ToLower(strings.TrimSpace(ing.Food.Name))) {
				out[i].Ingredients = append(out[i].Ingredients, ing)
			} else {
				left = append(left, ing)
			}
		}
		remaining = left
	}
	if remaining != nil {
		out[0].Ingredients = remaining
	}
	return out
}

// BuildPayload converts the recipe from /api/ai-import/ into a POST /api/recipe/ body. It
// returns a NoRecipeError when the AI found neither ingredients nor steps.
func BuildPayload(ai AIRecipe, sourceURL, caption, uploader string) (Payload, error) {
	var steps []Step
	for _, raw := range ai.Steps {
		instruction := strings.TrimSpace(raw.Instruction)
		ingredients := []Ingredient{}
		for _, rawIng := range raw.Ingredients {
			if ing, ok := convertIngredient(rawIng); ok {
				ingredients = append(ingredients, ing)
			}
		}
		if instruction == "" && len(ingredients) == 0 {
			continue
		}
		show := raw.ShowIngredientsTable == nil || *raw.ShowIngredientsTable
		steps = append(steps, Step{Instruction: instruction, Ingredients: ingredients, ShowIngredientsTable: show})
	}
	if len(steps) == 0 {
		return Payload{}, &NoRecipeError{"No recipe found in this reel."}
	}
	steps = assignIngredients(splitSingleStep(steps))

	name := strings.TrimSpace(ai.Name)
	if name == "" {
		name = FallbackName(caption, uploader)
	}
	// Tandoor's scraper turns a missing yield into "None"; Kitshn blanks anything under 6 characters.
	servingsText := strings.TrimSpace(ai.ServingsText)
	if utf8.RuneCountInString(servingsText) < 6 {
		servingsText = ""
	}
	keywords := []Named{}
	for _, kw := range ai.Keywords {
		if kw.Name != "" {
			keywords = append(keywords, Named{kw.Name})
		}
	}

	return Payload{
		Name:         truncate(name, nameMax),
		Description:  truncate(strings.TrimSpace(ai.Description), descriptionMax),
		Servings:     intOr(ai.Servings, 1),
		ServingsText: servingsText,
		WorkingTime:  intOr(ai.WorkingTime, 0),
		WaitingTime:  intOr(ai.WaitingTime, 0),
		SourceURL:    sourceURL,
		Internal:     true,
		Keywords:     keywords,
		Steps:        steps,
	}, nil
}
