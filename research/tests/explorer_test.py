# SPDX-License-Identifier: GPL-3.0-or-later

"""Smoke tests for the metrics explorer."""

# TODO: expand coverage as the explorer stabilises.

import importlib


def test_module_loads():
    mod = importlib.import_module("research.explorer")
    assert hasattr(mod, "main")
