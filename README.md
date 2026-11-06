# reel-to-tandoor

Share an Instagram reel from an iPhone and get a proper recipe in [Tandoor](https://tandoor.dev).

Kitshn's Instagram import only reads the post's caption. Plenty of recipe reels keep the whole
recipe in the narration and put "Comment 'yes' for my cookbook" in the caption, so the AI gets
nothing to work with and Tandoor refuses to save the empty result
([kitshn#302](https://github.com/kitshn-app/kitshn/issues/302)). This service listens to the video
as well.

## How it works

1. The iOS Shortcut sends the reel link to `POST /save` with the person's API key.
2. The service checks the link is an Instagram (or TikTok) post, then runs `yt-dlp` to download its
   audio track (about 300 KB for a 40 s reel), caption and cover image. No Instagram login is needed.
3. `ffmpeg` decodes the audio and [whisper.cpp](https://github.com/ggml-org/whisper.cpp) (`small`
   model, q8_0, CPU) transcribes it, skipping music and silence with Silero VAD.
4. The caption and transcript go to Tandoor's own AI import (`/api/ai-import/`), using the AI
   provider configured in Tandoor.
5. The result is saved with `POST /api/recipe/` in that person's Tandoor account, with the reel as
   its source URL and the cover image as its picture.
6. The Shortcut shows "Saved: Lentil Bread" and opens the recipe.

The payload clean-up follows Kitshn's
[`TandoorRecipeFromSource.create()`](https://github.com/kitshn-app/kitshn/blob/main/shared/src/commonMain/kotlin/de/kitshn/api/tandoor/model/recipe/TandoorRecipeFromSource.kt):
servings default to 1, short `servings_text` is blanked, `order: null` is dropped, a single
multi-line step is split up, and ingredients move to the step that mentions them. If the AI leaves
the name blank, the caption's first line that isn't a call to action is used. If it finds no
ingredients and no steps, nothing is saved and the Shortcut shows "No recipe found in this reel."

## API

`POST /save` with header `X-API-Key: <key>` and body `{"url": "<reel link>"}`. The `url` may be any
text containing the link, since some apps share "Look at this! https://…".

Every reply is JSON with `ok` and a human-readable `message`, whatever the status:

| Status | When | Example `message` |
| --- | --- | --- |
| 200 | Saved | `Saved: Lentil Bread` (plus `recipe_id`, `name`, `recipe_url`) |
| 400 | Not an Instagram/TikTok post | `Only Instagram and TikTok links are supported.` |
| 401 | Missing or unknown API key | `This Shortcut's API key isn't valid. Ask Saul for a new one.` |
| 422 | Nothing recipe-like in the caption or narration | `No recipe found in this reel.` |
| 502 | Download failed, or Tandoor or its AI failed | `Couldn't download that reel. Check the link and try again.` |

`GET /healthz` returns `{"ok": true}` once the whisper model is loaded.

## Configuration

All settings are environment variables.

| Variable | Default | Notes |
| --- | --- | --- |
| `REEL_USERS` | `{}` | **Required.** JSON mapping each API key to a Tandoor token: `{"<api key>": {"name": "mum", "tandoor_token": "tda_…"}}` |
| `TANDOOR_URL` | `http://tandoor.tandoor.svc.cluster.local` | Where to reach Tandoor's API |
| `TANDOOR_HOST_HEADER` | unset | Sent as `Host`. Needed when `TANDOOR_URL` isn't a host in Tandoor's `ALLOWED_HOSTS` |
| `TANDOOR_PUBLIC_URL` | `TANDOOR_URL` | Base of the link the Shortcut opens |
| `TANDOOR_AI_PROVIDER_ID` | `34` | Tandoor AI provider to use (Settings → AI in Tandoor shows the ids) |
| `TANDOOR_TIMEOUT` | `120` | Seconds to wait for Tandoor, mostly for the AI import |
| `ALLOW_TIKTOK` | `true` | Also accept TikTok links |
| `COOKIES_FILE` | unset | Netscape cookies file for yt-dlp, in case Instagram starts blocking anonymous downloads |
| `MAX_DURATION_SECONDS` | `600` | Longer videos are refused |
| `MAX_DOWNLOAD_MB` | `100` | Bigger downloads are refused |
| `WHISPER_MODEL` | `/models/ggml-small-q8_0.bin` | Path to a whisper.cpp ggml model. The image ships this one |
| `WHISPER_VAD_MODEL` | `/models/ggml-silero-v6.2.0.bin` in the image | Silero VAD model; leave empty to transcribe everything |
| `WHISPER_CPU_THREADS` | `4` | More threads made it slower on big.LITTLE arm64 boards |
| `WHISPER_BEAM_SIZE` | `1` | `1` decodes greedily; more uses beam search, which is slower |
| `YTDLP_PATH` | `yt-dlp` | `/opt/yt-dlp/yt-dlp` in the image |
| `FFMPEG_PATH` | `ffmpeg` | |
| `LISTEN_ADDR` | `:8080` | |

API keys are compared in constant time. Make them long and random:

```sh
openssl rand -hex 32
```

```powershell
-join ([Security.Cryptography.RandomNumberGenerator]::GetBytes(32) | ForEach-Object { $_.ToString('x2') })
```

### Getting a Tandoor token for someone

The token decides whose account the recipe lands in, so each person needs their own.

- **They can make it themselves:** in Tandoor, open Settings → API, tap **New**, set the scope to
  `read write` and the expiry a few years out, and copy the token.
- **An admin can make it for them:** open `/admin/oauth2_provider/accesstoken/add/` on the Tandoor
  site, pick the user, set the token to `tda_` followed by a random hex string, scope `read write`,
  and an expiry far in the future.

## Running it

The image is `ghcr.io/1solon/reel-to-tandoor` (linux/amd64 and linux/arm64), built by GitHub
Actions on every push to `main`. It listens on port 8080 as user 1000 and works with a read-only
root filesystem as long as `/tmp` is writable. Besides the Go binary it holds whisper.cpp (linked
in), a static ffmpeg, and yt-dlp's standalone build, which bundles its own Python: nothing else
keeps up with Instagram's changes.

```sh
docker run --rm -p 8080:8080 --read-only --tmpfs /tmp \
  -e TANDOOR_URL=https://recipes.example.com \
  -e REEL_USERS='{"my-long-random-key": {"name": "me", "tandoor_token": "tda_..."}}' \
  ghcr.io/1solon/reel-to-tandoor:latest

curl -X POST localhost:8080/save -H 'X-API-Key: my-long-random-key' \
  -H 'Content-Type: application/json' -d '{"url": "https://www.instagram.com/reel/DfXD-34Bf40/"}'
```

Give it about 1.5 GB of memory. Requests run one transcription at a time. A 40 s reel takes 15 to
20 seconds in total on a modern x86 CPU (the transcription is about 7 s of that), and about 50
seconds on an RK3588-class arm64 board (where transcription takes 38 s).

### Development

The service links whisper.cpp through cgo, so building it needs whisper.cpp's static libraries.
The Dockerfile's `test` stage builds them and runs gofmt, `go vet` and the tests:

```sh
docker build --target test .
```

To work outside Docker, build whisper.cpp v1.9.5 (the version `go.mod` pins the bindings to) and
point cgo at it:

```sh
git clone --depth 1 --branch v1.9.5 https://github.com/ggml-org/whisper.cpp /tmp/whisper.cpp
cmake -S /tmp/whisper.cpp -B /tmp/whisper.cpp/build -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_EXAMPLES=OFF
cmake --build /tmp/whisper.cpp/build -j
export CGO_ENABLED=1 C_INCLUDE_PATH=/tmp/whisper.cpp/include:/tmp/whisper.cpp/ggml/include \
  LIBRARY_PATH=/tmp/whisper.cpp/build/src:/tmp/whisper.cpp/build/ggml/src
go test ./...
```

Running it locally also needs `yt-dlp`, `ffmpeg` and a ggml model from
[ggerganov/whisper.cpp](https://huggingface.co/ggerganov/whisper.cpp) on Hugging Face.

## The iOS Shortcut

Build it on the iPhone that will use it. It takes about five minutes. You need the service's
address (for example `https://reel-to-tandoor.example.com`) and that person's API key.

1. Open the **Shortcuts** app, tap **+** to make a new shortcut, and name it **Save to Tandoor**.
2. Tap the **ⓘ** (details) button at the bottom, turn on **Show in Share Sheet**, then tap **Done**.
   A "Receive **Any** input from **Share Sheet**" block appears at the top. Tap **Any** and leave
   only **URLs** and **Text** selected.
3. Add the action **Get Contents of URL**.
   - Tap the URL field and type the service address followed by `/save`, e.g.
     `https://reel-to-tandoor.example.com/save`.
   - Tap the **>** arrow to show more options.
   - **Method:** `POST`.
   - **Headers:** tap **Add new header**. Key `X-API-Key`, value the person's API key.
   - **Request Body:** `JSON`. Tap **Add new field** → **Text**. Key `url`. For the value, tap the
     field, then pick **Shortcut Input** from the variables bar above the keyboard.
4. Add the action **Get Dictionary Value**. Set it to get the **Value** for key `message` in
   **Contents of URL**.
5. Add another **Get Dictionary Value**: **Value** for key `recipe_url` in **Contents of URL**.
6. Add an **If** action. Set the input to the second **Dictionary Value** (the `recipe_url` one) and
   the condition to **has any value**.
7. Inside the **If** branch:
   - Add **Show Notification**. Tap its text and pick the first **Dictionary Value** (`message`).
   - Add **Open URLs** and give it the second **Dictionary Value** (`recipe_url`). Leave this out
     if you'd rather not jump to the recipe every time.
8. Inside the **Otherwise** branch, add **Show Alert** and set its message to the first
   **Dictionary Value** (`message`). Turn off **Show Cancel Button**.
9. Tap **Done**.

To use it: in Instagram, tap the share (paper plane) button on a reel, tap **Share to…** (or
**More**), and pick **Save to Tandoor**. It takes 15 to 30 seconds. The first run asks for
permission to connect to the service; choose **Always Allow**.

If the share sheet doesn't list it, scroll to the end of the app row, tap **Edit Actions**, and add
**Save to Tandoor** to Favorites.

### Importable Shortcut (needs a Mac)

`cmd/build-shortcut` writes the same Shortcut as a file. iOS only imports signed Shortcuts, so it
has to be signed on a Mac first:

```sh
go run ./cmd/build-shortcut -url https://reel-to-tandoor.example.com -api-key KEY
shortcuts sign --mode anyone --input "Save to Tandoor.unsigned.shortcut" --output "Save to Tandoor.shortcut"
```

AirDrop the signed file to the phone. The generated file hasn't been tried on a device yet, so if
it won't import, build the Shortcut by hand as above.

## Troubleshooting

- **"Instagram wouldn't let the server download that reel."** Instagram has started asking for a
  login. Export cookies from a logged-in browser session (a throwaway account is best) in Netscape
  format, mount the file, and point `COOKIES_FILE` at it.
- **"Tandoor rejected this user's token."** The token expired or was deleted. Make a new one and
  update `REEL_USERS`.
- **"Too many recipe imports this hour."** Tandoor limits AI imports per user (`AI_RATELIMIT`,
  default 60 an hour).
- **The recipe has the wrong name.** The AI sometimes leaves the name blank, and the fallback takes
  it from the caption. Rename it in Tandoor.
