"""Write an unsigned "Save to Tandoor" Shortcut file.

iOS only imports signed Shortcuts, so sign the output on a Mac before sending it to a phone:

    uv run python shortcut/build_shortcut.py --url https://reel-to-tandoor.example.com --api-key KEY
    shortcuts sign --mode anyone --input "Save to Tandoor.unsigned.shortcut" --output "Save to Tandoor.shortcut"

Without a Mac, build the Shortcut by hand from the README instead.
"""

import argparse
import plistlib
import uuid

OBJ = "￼"  # Shortcuts' placeholder character for an inline variable.


def text(value: str) -> dict:
    return {"Value": {"string": value}, "WFSerializationType": "WFTextTokenString"}


def attachment(output_uuid: str, name: str) -> dict:
    return {"OutputUUID": output_uuid, "Type": "ActionOutput", "OutputName": name}


def text_with(variable: dict) -> dict:
    return {
        "Value": {"string": OBJ, "attachmentsByRange": {"{0, 1}": variable}},
        "WFSerializationType": "WFTextTokenString",
    }


def variable(output_uuid: str, name: str) -> dict:
    return {"Value": attachment(output_uuid, name), "WFSerializationType": "WFTextTokenAttachment"}


def dictionary(items: list[tuple[str, dict]]) -> dict:
    return {
        "Value": {
            "WFDictionaryFieldValueItems": [
                {"WFItemType": 0, "WFKey": text(key), "WFValue": value} for key, value in items
            ]
        },
        "WFSerializationType": "WFDictionaryFieldValue",
    }


def action(identifier: str, **params) -> dict:
    return {"WFWorkflowActionIdentifier": f"is.workflow.actions.{identifier}", "WFWorkflowActionParameters": params}


def build(service_url: str, api_key: str) -> dict:
    request_id, message_id, link_id = (str(uuid.uuid4()).upper() for _ in range(3))
    group = str(uuid.uuid4()).upper()

    actions = [
        action(
            "downloadurl",
            UUID=request_id,
            WFURL=service_url.rstrip("/") + "/save",
            WFHTTPMethod="POST",
            ShowHeaders=True,
            WFHTTPHeaders=dictionary([("X-API-Key", text(api_key))]),
            WFHTTPBodyType="JSON",
            WFJSONValues=dictionary([("url", text_with({"Type": "ExtensionInput"}))]),
        ),
        action(
            "getvalueforkey",
            UUID=message_id,
            CustomOutputName="Message",
            WFDictionaryKey="message",
            WFInput=variable(request_id, "Contents of URL"),
        ),
        action(
            "getvalueforkey",
            UUID=link_id,
            CustomOutputName="Recipe Link",
            WFDictionaryKey="recipe_url",
            WFInput=variable(request_id, "Contents of URL"),
        ),
        action(
            "conditional",
            GroupingIdentifier=group,
            WFControlFlowMode=0,
            WFCondition=100,  # "has any value"
            WFInput={"Type": "Variable", "Variable": variable(link_id, "Recipe Link")},
        ),
        action(
            "notification",
            WFNotificationActionTitle="Tandoor",
            WFNotificationActionBody=text_with(attachment(message_id, "Message")),
        ),
        action("openurl", WFInput=variable(link_id, "Recipe Link")),
        action("conditional", GroupingIdentifier=group, WFControlFlowMode=1),
        action(
            "alert",
            WFAlertActionTitle="Couldn't save the recipe",
            WFAlertActionMessage=text_with(attachment(message_id, "Message")),
            WFAlertActionCancelButtonShown=False,
        ),
        action("conditional", GroupingIdentifier=group, WFControlFlowMode=2),
    ]

    return {
        "WFWorkflowClientVersion": "2607.0.2",
        "WFWorkflowMinimumClientVersion": 900,
        "WFWorkflowMinimumClientVersionString": "900",
        "WFWorkflowIcon": {"WFWorkflowIconStartColor": 4292093695, "WFWorkflowIconGlyphNumber": 59511},
        "WFWorkflowTypes": ["ActionExtension"],
        "WFWorkflowInputContentItemClasses": ["WFURLContentItem", "WFStringContentItem"],
        "WFWorkflowHasShortcutInputVariables": True,
        "WFWorkflowImportQuestions": [],
        "WFWorkflowActions": actions,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument(
        "--url", required=True, help="Base URL of the service, e.g. https://reel-to-tandoor.example.com"
    )
    parser.add_argument("--api-key", default="PASTE-YOUR-API-KEY", help="The person's API key")
    parser.add_argument("--out", default="Save to Tandoor.unsigned.shortcut")
    args = parser.parse_args()
    with open(args.out, "wb") as fh:
        plistlib.dump(build(args.url, args.api_key), fh, fmt=plistlib.FMT_BINARY)
    print(f"Wrote {args.out}. Sign it on a Mac with `shortcuts sign` before importing.")


if __name__ == "__main__":
    main()
