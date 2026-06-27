#!/bin/bash
set -euxo pipefail
exec uv run streamlit run research/explorer.py
