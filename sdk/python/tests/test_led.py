"""The LED patterns of docs/devices.md ("Pattern definitions").

The shared cases in sdk/testdata/led_cases.json come from the FMS's reference renderer, static/js/led_patterns.js
(regenerate them with `node sdk/testdata/make_led_cases.js`). The other tests here spell out the definitions.
"""

import json
import os

import pytest

from galaxia.led import BLACK, animated, js_round, parse_color, render

RED = (255, 0, 0)
BLUE = (0, 0, 255)
T0 = 1_794_510_000_000
CASES = os.path.join(os.path.dirname(__file__), "..", "..", "testdata", "led_cases.json")


def test_shared_cases_from_the_reference_renderer():
    with open(CASES) as handle:
        cases = json.load(handle)
    assert len(cases) > 80
    for case in cases:
        got = render(case["cmd"], case["pixels"], case["t"])
        assert [list(color) for color in got] == case["frame"], case


def test_parse_color():
    assert parse_color("#ff8000") == (255, 128, 0)
    assert parse_color("FF8000") == (255, 128, 0)
    for bad in ("#ff80", "red", None, 7, "#gg0000"):
        assert parse_color(bad) == BLACK


def test_js_round_rounds_halves_up_like_math_round():
    assert [js_round(x) for x in (0.5, 1.5, 2.5, 2.4999, -0.5, -1.5, 0.49999999999999994)] == [1, 2, 3, 2, 0, -1, 0]


def test_off_solid_and_unknown():
    assert render({"p": "off", "c": ["#ff0000"]}, 3, T0) == [BLACK] * 3
    assert render({"p": "solid", "c": ["#ff0000"]}, 3, T0) == [RED] * 3
    assert render({"p": "sparkle", "c": ["#ff0000"]}, 2, T0) == [BLACK] * 2
    assert render({}, 2, T0) == [BLACK] * 2


@pytest.mark.parametrize("t,expected", [(0, RED), (499, RED), (500, BLUE), (999, BLUE), (1000, RED)])
def test_blink_is_the_first_colour_for_the_first_half_of_each_period(t, expected):
    assert render({"p": "blink", "c": ["#ff0000", "#0000ff"], "ms": 1000}, 1, T0 + t) == [expected]


def test_default_periods():
    assert render({"p": "blink", "c": ["#ff0000"]}, 1, T0 + 249) == [RED]
    assert render({"p": "blink", "c": ["#ff0000"]}, 1, T0 + 250) == [BLACK]
    assert render({"p": "pulse", "c": ["#ff0000"]}, 1, T0 + 750) == [RED]  # 1500: full at half the period
    assert render({"p": "chase", "c": ["#ff0000"]}, 8, T0 + 125)[1] == RED  # 1000: one LED per 125 ms here
    assert render({"p": "rainbow"}, 1, T0 + 1500) == [(0, 255, 255)]  # 3000: cyan half way


def test_pulse_goes_from_the_second_colour_to_the_first_and_back():
    command = {"p": "pulse", "c": ["#ff0000", "#0000ff"], "ms": 1000}
    assert render(command, 1, T0) == [BLUE]
    assert render(command, 1, T0 + 500) == [RED]
    assert render(command, 1, T0 + 1000) == [BLUE]


def test_chase_runs_a_block_of_an_eighth_of_the_strip_once_per_period():
    command = {"p": "chase", "c": ["#ff0000"], "ms": 800}
    assert render(command, 16, T0) == [RED, RED] + [BLACK] * 14
    assert render(command, 16, T0 + 100) == [BLACK, BLACK, RED, RED] + [BLACK] * 12
    assert render(command, 16, T0 + 750) == [RED] + [BLACK] * 14 + [RED]  # wraps round
    assert render(command, 3, T0) == [RED, BLACK, BLACK]  # at least one LED


def test_rainbow_spreads_the_hues_along_the_strip():
    frame = render({"p": "rainbow", "ms": 1000}, 6, T0)
    assert frame[0] == (255, 0, 0)
    assert frame[1] == (255, 255, 0)
    assert frame[3] == (0, 255, 255)


@pytest.mark.parametrize("v,lit", [(0, 0), (0.04, 0), (0.05, 1), (0.25, 3), (0.5, 5), (0.55, 6), (1, 10), (3, 10)])
def test_progress_lights_round_v_times_n(v, lit):
    frame = render({"p": "progress", "c": ["#ff0000", "#0000ff"], "v": v}, 10, T0)
    assert frame == [RED] * lit + [BLUE] * (10 - lit)


def test_segments_are_clipped_to_the_strip_and_the_rest_is_off():
    command = {
        "p": "solid",
        "c": ["#00ff00"],
        "seg": [
            {"s": 0, "n": 2, "p": "solid", "c": ["#ff0000"]},
            {"s": 5, "n": 10, "p": "progress", "c": ["#0000ff"], "v": 0.4},
        ],
    }
    # The second segment covers LEDs 5 to 7 only, so its fill is 0.4 x 3 rounded: one LED.
    assert render(command, 8, T0) == [RED, RED, BLACK, BLACK, BLACK, BLUE, BLACK, BLACK]
    # An empty segment list means the top-level look.
    assert render({"p": "solid", "c": ["#00ff00"], "seg": []}, 2, T0) == [(0, 255, 0)] * 2


def test_brightness_multiplies_last_and_rounds():
    assert render({"p": "solid", "c": ["#ff8000"], "br": 0.5}, 1, T0) == [(128, 64, 0)]
    assert render({"p": "solid", "c": ["#ffffff"], "br": 0.3}, 1, T0) == [(77, 77, 77)]  # 255 x 0.3 is 76.5
    assert render({"p": "solid", "c": ["#ffffff"], "br": 4}, 1, T0) == [(255, 255, 255)]
    # The SDK's own cap multiplies br.
    assert render({"p": "solid", "c": ["#ff8000"], "br": 0.5}, 1, T0, limit=0.5) == [(64, 32, 0)]


def test_animated():
    assert animated({"p": "blink"}) and animated({"seg": [{"p": "solid"}, {"p": "chase"}]})
    assert not animated({"p": "progress"}) and not animated(None) and not animated({"p": "pulse", "seg": [{}]})
