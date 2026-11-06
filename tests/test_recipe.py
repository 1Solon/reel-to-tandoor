import copy
import json
from pathlib import Path

import pytest

from reel_to_tandoor.recipe import (
    NoRecipeFound,
    build_recipe_payload,
    clean_caption,
    compose_ai_text,
    fallback_name,
)

SOURCE = "https://www.instagram.com/reel/DfXD-34Bf40/"
CAPTION = "Comment “yes” for my new family cookbook! \n\n#wellnesstips #health #wellnessjourney"


@pytest.fixture
def lentil_bread():
    """The real /api/ai-import/ response for the test reel (GLM-5.3 Flash, 2026-11-06)."""
    path = Path(__file__).parent / "fixtures" / "ai_import_lentil_bread.json"
    return json.loads(path.read_text())["recipe"]


def ingredient(food, unit=None, amount=1.0, note="", order=None):
    return {
        "amount": amount,
        "food": {"name": food},
        "unit": {"name": unit} if unit else None,
        "note": note,
        "order": order,
        "original_text": f"{amount} {unit or ''} {food}".strip(),
    }


def test_clean_caption_drops_hashtags_and_blank_lines():
    assert clean_caption(CAPTION) == "Comment “yes” for my new family cookbook!"
    assert clean_caption(None) == ""
    assert clean_caption("Lentil bread #vegan\n\n#glutenfree") == "Lentil bread"


def test_compose_ai_text_includes_caption_and_transcript():
    text = compose_ai_text(CAPTION, "Soak two cups of red lentils overnight.")
    assert text == (
        "Caption:\nComment “yes” for my new family cookbook!\n\n"
        "Transcript of the video's narration:\nSoak two cups of red lentils overnight."
    )


def test_compose_ai_text_works_with_only_one_source():
    assert compose_ai_text("", "Soak lentils.") == "Transcript of the video's narration:\nSoak lentils."
    assert compose_ai_text("2 cups lentils, bake 45 min", "  ") == "Caption:\n2 cups lentils, bake 45 min"


def test_compose_ai_text_refuses_empty_reel():
    with pytest.raises(NoRecipeFound):
        compose_ai_text("#food #yum", "")


@pytest.mark.parametrize(
    ("caption", "expected"),
    [
        ("Easy lentil bread 🍞 #vegan", "Easy lentil bread 🍞"),
        ("Comment 'yes' for my cookbook!\nHigh-protein lentil bread. So good!", "High-protein lentil bread"),
        ("@someone\nCrispy chickpea wraps", "Crispy chickpea wraps"),
        (CAPTION, "Recipe from cameronjames.wellness"),
        ("", "Recipe from cameronjames.wellness"),
    ],
)
def test_fallback_name(caption, expected):
    assert fallback_name(caption, "cameronjames.wellness") == expected


def test_fallback_name_without_uploader():
    assert fallback_name("Follow for more") == "Recipe from a reel"


def test_payload_from_real_ai_response(lentil_bread):
    payload = build_recipe_payload(lentil_bread, SOURCE, caption=CAPTION, uploader="cameronjames.wellness")

    assert payload["name"] == "Flourless Lentil Bread"
    assert payload["source_url"] == SOURCE
    assert payload["servings"] == 1  # AI returned null
    assert payload["servings_text"] == ""  # AI returned the string "None"
    assert payload["internal"] is True
    assert payload["keywords"] == [{"name": "✨ AI"}]
    assert [s["instruction"] for s in payload["steps"]][0] == "Soak two cups of red lentils overnight."

    by_step = [[i["food"]["name"] for i in s["ingredients"]] for s in payload["steps"]]
    assert by_step == [
        ["red lentils"],
        ["fresh water", "baking powder", "salt"],
        ["seeds"],
        [],
        [],
    ]
    for step in payload["steps"]:
        for ing in step["ingredients"]:
            assert "order" not in ing


def test_payload_keeps_set_order():
    recipe = {"name": "X", "steps": [{"instruction": "Mix", "ingredients": [ingredient("flour", order=2)]}]}
    assert build_recipe_payload(recipe, SOURCE)["steps"][0]["ingredients"][0]["order"] == 2


def test_payload_falls_back_when_name_blank(lentil_bread):
    lentil_bread["name"] = "  "
    payload = build_recipe_payload(lentil_bread, SOURCE, caption="Lentil bread, no flour! #vegan", uploader="c")
    assert payload["name"] == "Lentil bread, no flour"


def test_payload_splits_food_note():
    recipe = {
        "name": "Bread",
        "steps": [{"instruction": "Top", "ingredients": [ingredient("seeds (for topping)", note="optional")]}],
    }
    ing = build_recipe_payload(recipe, SOURCE)["steps"][0]["ingredients"][0]
    assert ing["food"] == {"name": "seeds"}
    assert ing["note"] == "for topping, optional"


def test_payload_splits_single_multiline_step():
    recipe = {
        "name": "Bread",
        "steps": [
            {
                "instruction": "Soak the lentils.\n\nBlend with water.\nBake.",
                "ingredients": [ingredient("lentils", "cups", 2), ingredient("water", "cup")],
            }
        ],
    }
    steps = build_recipe_payload(recipe, SOURCE)["steps"]
    assert [s["instruction"] for s in steps] == ["Soak the lentils.", "Blend with water.", "Bake."]
    assert [[i["food"]["name"] for i in s["ingredients"]] for s in steps] == [["lentils"], ["water"], []]


def test_payload_handles_duplicate_ingredients():
    recipe = {
        "name": "Two sauces",
        "steps": [
            {"instruction": "Gather", "ingredients": [ingredient("salt"), ingredient("salt")]},
            {"instruction": "Add salt", "ingredients": []},
        ],
    }
    steps = build_recipe_payload(recipe, SOURCE)["steps"]
    assert len(steps[0]["ingredients"]) == 0
    assert len(steps[1]["ingredients"]) == 2


def test_payload_drops_blank_ingredients_and_steps():
    recipe = {
        "name": "Bread",
        "steps": [
            {"instruction": "", "ingredients": []},
            {"instruction": "Bake", "ingredients": [ingredient(""), {"food": None, "amount": 1}, ingredient("flour")]},
        ],
    }
    steps = build_recipe_payload(recipe, SOURCE)["steps"]
    assert len(steps) == 1
    assert [i["food"]["name"] for i in steps[0]["ingredients"]] == ["flour"]


def test_payload_keeps_long_servings_text_and_truncates_fields(lentil_bread):
    recipe = copy.deepcopy(lentil_bread)
    recipe.update(servings=8, servings_text="8 slices", name="n" * 300, description="d" * 900)
    payload = build_recipe_payload(recipe, SOURCE)
    assert payload["servings"] == 8
    assert payload["servings_text"] == "8 slices"
    assert len(payload["name"]) == 128
    assert len(payload["description"]) == 512


@pytest.mark.parametrize(
    "recipe",
    [
        {"name": "", "steps": []},
        {"name": "Lentil Bread", "steps": []},
        {"name": "", "steps": [{"instruction": " ", "ingredients": []}]},
        {},
    ],
)
def test_payload_refuses_empty_recipe(recipe):
    with pytest.raises(NoRecipeFound, match="No recipe found in this reel"):
        build_recipe_payload(recipe, SOURCE)
