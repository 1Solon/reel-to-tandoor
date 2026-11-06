"""Turn a reel's caption and transcript into the text for Tandoor's AI import, and turn the AI's
answer into a payload ``POST /api/recipe/`` accepts.

Payload rules follow Kitshn's ``TandoorRecipeFromSource.create()``:
https://github.com/kitshn-app/kitshn/blob/main/shared/src/commonMain/kotlin/de/kitshn/api/tandoor/model/recipe/TandoorRecipeFromSource.kt
"""

import re
from typing import Any

# Tandoor model field limits (cookbook/models.py).
NAME_MAX = 128
DESCRIPTION_MAX = 512

_HASHTAG_RE = re.compile(r"(?<!\w)#[\w-]+")
_MENTION_ONLY_RE = re.compile(r"^(@[\w.]+\s*)+$")
# Lines that are calls to action rather than a dish name.
_CTA_RE = re.compile(
    r"\b(comment|follow|link in (my )?bio|save (this|it)|share (this|it)|dm\b|subscribe|giveaway|tag a)",
    re.IGNORECASE,
)


class NoRecipeFound(Exception):
    """The reel had no recipe in its caption or narration."""


def clean_caption(caption: str | None) -> str:
    """Drop hashtags and blank lines; they add noise to the AI prompt and never hold the recipe."""
    lines = []
    for line in (caption or "").splitlines():
        line = _HASHTAG_RE.sub("", line).strip()
        if line:
            lines.append(line)
    return "\n".join(lines)


def compose_ai_text(caption: str | None, transcript: str | None) -> str:
    """Build the ``text`` field for ``/api/ai-import/``.

    Raises ``NoRecipeFound`` when there's nothing to send, so we don't spend an AI call on it.
    """
    caption = clean_caption(caption)
    transcript = (transcript or "").strip()
    if not caption and not transcript:
        raise NoRecipeFound("No recipe found in this reel (it has no caption and no narration).")

    # No uploader line: GLM turns the creator's handle into a keyword.
    parts = []
    if caption:
        parts.append(f"Caption:\n{caption}")
    if transcript:
        parts.append(f"Transcript of the video's narration:\n{transcript}")
    return "\n\n".join(parts)


def fallback_name(caption: str | None, uploader: str | None = None) -> str:
    """A name for when the AI leaves ``name`` blank (GLM does this for about half of all captions).

    Uses the caption's first line that isn't a call to action, else "Recipe from <uploader>".
    """
    for line in clean_caption(caption).splitlines():
        line = line.strip(" -–—:|•*")
        if not line or _MENTION_ONLY_RE.match(line) or _CTA_RE.search(line):
            continue
        # First sentence only; captions often run on into the story behind the dish.
        line = re.split(r"(?<=[.!?])\s", line, maxsplit=1)[0].rstrip(".!? ")
        if 3 <= len(line) <= 80:
            return line
    return f"Recipe from {uploader}" if uploader else "Recipe from a reel"


def _ingredient(raw: dict[str, Any]) -> dict[str, Any] | None:
    food = (raw.get("food") or {}).get("name") or ""
    food = food.strip()
    note = (raw.get("note") or "").strip()
    if not food:
        return None

    # "seeds (for topping)" -> food "seeds", note "for topping". Kitshn does the same so foods
    # don't get created once per note.
    if " (" in food:
        food, extra = food.split(" (", 1)
        extra = extra.rstrip(")").strip()
        note = f"{extra}, {note}" if note and extra else (extra or note)

    unit = raw.get("unit")
    unit_name = ((unit or {}).get("name") or "").strip()

    ingredient: dict[str, Any] = {
        "food": {"name": food[:NAME_MAX]},
        "unit": {"name": unit_name[:NAME_MAX]} if unit_name else None,
        "amount": raw.get("amount") or 0,
        "note": note,
        "original_text": raw.get("original_text") or "",
    }
    # ``order: null`` makes the API reject the recipe, so "order" is only sent when it's set.
    if raw.get("order") is not None:
        ingredient["order"] = raw["order"]
    return ingredient


def _split_single_step(steps: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """If everything landed in one step, split it into one step per line, like Kitshn."""
    if len(steps) != 1:
        return steps
    lines = [line.strip() for line in steps[0]["instruction"].split("\n")]
    lines = [line for line in lines if line]
    if len(lines) <= 1:
        return steps
    first = {**steps[0], "instruction": lines[0]}
    return [first] + [{"instruction": line, "ingredients": [], "show_ingredients_table": True} for line in lines[1:]]


def _assign_ingredients(steps: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Move each ingredient to the first later step that mentions it; the rest go on step 1.

    The AI usually lists every ingredient on step 1. Same heuristic as Kitshn's autoSortIngredients.
    """
    if len(steps) <= 1:
        return steps
    remaining = [ing for step in steps for ing in step["ingredients"]]
    result = [{**step, "ingredients": []} for step in steps]
    for step in result[1:]:
        text = step["instruction"].lower()
        matched = [ing for ing in remaining if ing["food"]["name"].strip().lower() in text]
        step["ingredients"] = matched
        matched_ids = {id(ing) for ing in matched}
        remaining = [ing for ing in remaining if id(ing) not in matched_ids]
    result[0]["ingredients"] = remaining
    return result


def build_recipe_payload(
    ai_recipe: dict[str, Any],
    source_url: str,
    caption: str | None = None,
    uploader: str | None = None,
) -> dict[str, Any]:
    """Convert the ``recipe`` object from ``/api/ai-import/`` into a ``POST /api/recipe/`` body.

    Raises ``NoRecipeFound`` when the AI found neither ingredients nor steps.
    """
    steps = []
    for raw_step in ai_recipe.get("steps") or []:
        instruction = (raw_step.get("instruction") or "").strip()
        ingredients = [i for i in map(_ingredient, raw_step.get("ingredients") or []) if i]
        if not instruction and not ingredients:
            continue
        steps.append(
            {
                "instruction": instruction,
                "ingredients": ingredients,
                "show_ingredients_table": raw_step.get("show_ingredients_table", True),
            }
        )

    if not any(step["ingredients"] or step["instruction"] for step in steps):
        raise NoRecipeFound("No recipe found in this reel.")

    steps = _assign_ingredients(_split_single_step(steps))

    name = (ai_recipe.get("name") or "").strip() or fallback_name(caption, uploader)
    servings_text = (ai_recipe.get("servings_text") or "").strip()
    # Tandoor's scraper turns a missing yield into "None"; Kitshn blanks anything under 6 characters.
    if len(servings_text) < 6:
        servings_text = ""

    keywords = [
        {"name": kw["name"]} for kw in ai_recipe.get("keywords") or [] if isinstance(kw, dict) and kw.get("name")
    ]

    return {
        "name": name[:NAME_MAX],
        "description": (ai_recipe.get("description") or "").strip()[:DESCRIPTION_MAX],
        "servings": ai_recipe.get("servings") or 1,
        "servings_text": servings_text,
        "working_time": ai_recipe.get("working_time") or 0,
        "waiting_time": ai_recipe.get("waiting_time") or 0,
        "source_url": source_url,
        "internal": True,
        "keywords": keywords,
        "steps": steps,
    }
