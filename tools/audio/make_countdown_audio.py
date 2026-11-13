"""Builds the match-start countdown sounds: "Drivers behind the lines", "3", "2", "1", "GO!", and a riser.

Everything here is original to Galaxia FMS:
- The voice is generated offline with Piper (https://github.com/OHF-Voice/piper1-gpl) using the en_US-ljspeech-high
  voice. Its dataset and model are public domain (see LJSPEECH_MODEL_CARD.md beside this script). Piper is used as
  a tool only; nothing from it is shipped except the audio it produced.
- The impacts, whooshes and riser are synthesised below from noise and sine waves.

Each beat file starts exactly on its beat: sample 0 is the moment the beat lands. The riser is exactly
RISER_SECONDS long and ends on "GO!". The browser schedules them against the shared match clock, so the timing in
these files is part of the countdown's contract; static/audio/countdown/countdown.json describes it.

Usage, from the repository root (Piper installed in the Python that runs this, voice downloaded to VOICE_DIR):
    python -I tools/audio/make_countdown_audio.py <path to en_US-ljspeech-high.onnx>
"""

import io
import json
import os
import subprocess
import sys
import tempfile
import wave

import numpy as np

RATE = 48000
RISER_SECONDS = 3.0
OUTPUT_DIR = os.path.join("static", "audio", "countdown")
SEED = 254  # The synthesis is random noise; a fixed seed makes the files reproducible.

LINES = {
    "intro": "Drivers, behind the lines.",
    "3": "Three!",
    "2": "Two!",
    "1": "One!",
    "go": "Go!",
}


def speak(model, text, length_scale):
    """Runs Piper and returns the speech as float samples at RATE, trimmed to start at the first sound."""
    with tempfile.TemporaryDirectory() as directory:
        path = os.path.join(directory, "line.wav")
        command = [
            sys.executable, "-m", "piper", "-m", model, "-f", path, "--length-scale", str(length_scale),
            "--sentence-silence", "0", "--", text,
        ]
        subprocess.run(command, check=True, capture_output=True)
        with open(path, "rb") as handle:
            wav_bytes = handle.read()
    with wave.open(io.BytesIO(wav_bytes)) as reader:
        source_rate = reader.getframerate()
        samples = np.frombuffer(reader.readframes(reader.getnframes()), dtype=np.int16).astype(np.float64) / 32768
    samples = resample(samples, source_rate, RATE)
    return trim(samples)


def resample(samples, source_rate, target_rate):
    """Band-limited resampling by zero-padding in the frequency domain."""
    target_length = int(round(len(samples) * target_rate / source_rate))
    spectrum = np.fft.rfft(samples)
    padded = np.zeros(target_length // 2 + 1, dtype=complex)
    padded[: len(spectrum)] = spectrum
    return np.fft.irfft(padded, target_length) * target_length / len(samples)


def trim(samples, threshold=0.02):
    """Drops silence before the first sound and after the last, keeping a short tail."""
    loud = np.nonzero(np.abs(samples) > threshold)[0]
    if len(loud) == 0:
        return samples
    start = max(0, loud[0] - int(0.004 * RATE))
    end = min(len(samples), loud[-1] + int(0.12 * RATE))
    return samples[start:end]


def envelope(length, attack, decay):
    """An exponential decay with a linear attack, in seconds."""
    t = np.arange(length) / RATE
    env = np.exp(-t / decay)
    attack_samples = max(1, int(attack * RATE))
    env[:attack_samples] *= np.linspace(0, 1, attack_samples)
    return env


def lowpass(samples, cutoff):
    """A one-pole low-pass filter; enough to shape noise."""
    alpha = 1 - np.exp(-2 * np.pi * cutoff / RATE)
    out = np.empty_like(samples)
    state = 0.0
    for i, value in enumerate(samples):
        state += alpha * (value - state)
        out[i] = state
    return out


def sweep_noise(rng, seconds, start_cutoff, end_cutoff):
    """Noise through a low-pass whose cutoff sweeps: the body of a whoosh."""
    length = int(seconds * RATE)
    noise = rng.standard_normal(length)
    cutoffs = np.geomspace(start_cutoff, end_cutoff, length)
    out = np.empty(length)
    state = 0.0
    for i in range(length):
        alpha = 1 - np.exp(-2 * np.pi * cutoffs[i] / RATE)
        state += alpha * (noise[i] - state)
        out[i] = state
    return out / (np.max(np.abs(out)) + 1e-9)


def impact(rng, seconds, pitch, weight):
    """A hit: a falling sine thump, a noise crack and a short rumble."""
    length = int(seconds * RATE)
    t = np.arange(length) / RATE
    frequency = pitch * (1 + 1.5 * np.exp(-t / 0.03))
    thump = np.sin(2 * np.pi * np.cumsum(frequency) / RATE) * envelope(length, 0.002, 0.22 * weight)
    crack = lowpass(rng.standard_normal(length), 3500) * envelope(length, 0.0005, 0.025)
    rumble = lowpass(rng.standard_normal(length), 180) * envelope(length, 0.01, 0.5 * weight)
    return 1.0 * thump + 0.9 * crack / (np.max(np.abs(crack)) + 1e-9) + 0.6 * rumble / (np.max(np.abs(rumble)) + 1e-9)


def reverb(samples, seconds, mix):
    """A plain feedback-delay reverb tail, enough to put the voice in a room."""
    delays = [0.0297, 0.0371, 0.0411, 0.0437]
    tail_length = len(samples) + int(seconds * RATE)
    dry = np.zeros(tail_length)
    dry[: len(samples)] = samples
    wet = np.zeros(tail_length)
    for delay in delays:
        step = int(delay * RATE)
        gain = 10 ** (-3 * delay / seconds)
        line = np.zeros(tail_length)
        for i in range(step, tail_length):
            line[i] = dry[i - step] + gain * line[i - step]
        wet += line
    wet /= len(delays)
    return dry + mix * wet


def mix(*layers):
    """Adds layers that all start at sample 0."""
    length = max(len(layer) for layer in layers)
    out = np.zeros(length)
    for layer in layers:
        out[: len(layer)] += layer
    return out


def normalise(samples, peak_db=-1.0):
    peak = np.max(np.abs(samples)) + 1e-9
    return samples * (10 ** (peak_db / 20) / peak)


def fade_out(samples, seconds):
    length = min(len(samples), int(seconds * RATE))
    samples = samples.copy()
    samples[-length:] *= np.linspace(1, 0, length)
    return samples


def write(name, samples):
    path = os.path.join(OUTPUT_DIR, name)
    data = (np.clip(samples, -1, 1) * 32767).astype(np.int16)
    with wave.open(path, "wb") as writer:
        writer.setnchannels(1)
        writer.setsampwidth(2)
        writer.setframerate(RATE)
        writer.writeframes(data.tobytes())
    print("wrote", path, f"{len(samples) / RATE:.2f}s")


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    model = sys.argv[1]
    if not os.path.isdir(os.path.join("static", "audio")):
        sys.exit("Run this from the repository root.")
    os.makedirs(OUTPUT_DIR, exist_ok=True)
    rng = np.random.default_rng(SEED)

    # "Drivers, behind the lines": spoken a little slower, in a room.
    intro = reverb(speak(model, LINES["intro"], 1.15), 1.2, 0.25)
    write("intro.wav", normalise(fade_out(intro, 0.4), -2.0))

    # The beats: a hit under each number, the hits getting heavier towards "GO!".
    for number, pitch, weight in (("3", 62, 0.8), ("2", 58, 0.9), ("1", 54, 1.0)):
        voice = reverb(speak(model, LINES[number], 0.95), 0.8, 0.2)
        hit = impact(rng, 1.2, pitch, weight)
        write(f"{number}.wav", normalise(fade_out(mix(0.75 * hit, 0.9 * voice), 0.3)))

    # "GO!": the biggest hit, a shockwave of noise falling from bright to dark, and the voice on top.
    voice = reverb(speak(model, LINES["go"], 0.9), 1.4, 0.3)
    hit = impact(rng, 2.2, 45, 1.6)
    shockwave = sweep_noise(rng, 2.2, 9000, 120) * envelope(int(2.2 * RATE), 0.004, 0.6)
    write("go.wav", normalise(fade_out(mix(0.8 * hit, 0.5 * shockwave, 1.0 * voice), 0.6)))

    # The riser: noise and a rising tone that swell for exactly RISER_SECONDS and stop on "GO!".
    length = int(RISER_SECONDS * RATE)
    swell = np.linspace(0, 1, length) ** 2.5
    tone_frequency = np.geomspace(110, 880, length)
    tone = np.sin(2 * np.pi * np.cumsum(tone_frequency) / RATE)
    riser = (0.7 * sweep_noise(rng, RISER_SECONDS, 300, 9000) + 0.35 * tone) * swell
    riser[-int(0.004 * RATE):] *= np.linspace(1, 0, int(0.004 * RATE))  # No click at the cut.
    write("riser.wav", normalise(riser, -4.0))

    # What the browser needs to schedule them. Offsets are in seconds relative to the moment named by "anchor".
    description = {
        "sampleRate": RATE,
        "sounds": {
            "intro": {"file": "intro.wav", "anchor": "start", "note": "played before the first beat"},
            "3": {"file": "3.wav", "anchor": "beat", "offset": 0},
            "2": {"file": "2.wav", "anchor": "beat", "offset": 0},
            "1": {"file": "1.wav", "anchor": "beat", "offset": 0},
            "go": {"file": "go.wav", "anchor": "go", "offset": 0},
            "riser": {"file": "riser.wav", "anchor": "go", "offset": -RISER_SECONDS},
        },
    }
    with open(os.path.join(OUTPUT_DIR, "countdown.json"), "w", encoding="utf-8", newline="\n") as handle:
        json.dump(description, handle, indent=2)
        handle.write("\n")
    print("wrote", os.path.join(OUTPUT_DIR, "countdown.json"))


if __name__ == "__main__":
    main()
