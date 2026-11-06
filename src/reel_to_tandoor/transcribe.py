"""Speech to text with faster-whisper on the CPU."""

import logging
import threading

import numpy as np

log = logging.getLogger(__name__)


class Transcriber:
    """Loads the model once and runs one transcription at a time.

    Whisper already uses every thread it's given, so running two at once only makes both slower.
    """

    def __init__(self, model: str, model_dir: str | None, cpu_threads: int, compute_type: str, beam_size: int = 5):
        self._args = {"model_size_or_path": model, "download_root": model_dir}
        self._cpu_threads = cpu_threads
        self._compute_type = compute_type
        self._beam_size = beam_size
        self._model = None
        self._lock = threading.Lock()

    def load(self):
        if self._model is None:
            from faster_whisper import WhisperModel

            log.info("Loading whisper model %s", self._args["model_size_or_path"])
            self._model = WhisperModel(
                **self._args, device="cpu", compute_type=self._compute_type, cpu_threads=self._cpu_threads
            )
        return self._model

    def transcribe(self, audio: np.ndarray) -> str:
        if audio.size == 0:
            return ""
        with self._lock:
            model = self.load()
            segments, info = model.transcribe(audio, vad_filter=True, beam_size=self._beam_size)
            text = " ".join(segment.text.strip() for segment in segments)
        log.info("Transcribed %.0f s of %s audio into %d characters", info.duration, info.language, len(text))
        return text.strip()
