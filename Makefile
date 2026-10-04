# uspace-lab developer targets. CI (.github/workflows/) runs the same
# commands. One target block per work package (docs/PLAN.md §2). On
# Windows set GOROOT and GO, for example:
#   make test GO=/c/Users/<you>/AppData/Local/anaconda3/go/bin/go
GO   ?= go
PKGS ?= ./...

# Linter versions pinned to uspace-core's and to what CI runs (docs/PLAN.md
# §6). Change the Makefile and the workflow together. `make tools`
# installs them into $(go env GOPATH)/bin.
GOLANGCI_LINT_VERSION ?= v2.14.0
STATICCHECK_VERSION   ?= v0.8.1

.PHONY: build vet fmt fmt-check tools staticcheck lint test tidy

build:
	$(GO) build $(PKGS)

vet:
	$(GO) vet $(PKGS)

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

staticcheck:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) $(PKGS)

# Refuses a golangci-lint other than the pinned one: a different version
# enables different checks and would pass locally but fail in CI.
lint: fmt-check vet staticcheck
	@v="v$$(golangci-lint version --short 2>/dev/null)"; \
	if [ "$$v" != "$(GOLANGCI_LINT_VERSION)" ]; then \
	  echo "golangci-lint $$v found, CI runs $(GOLANGCI_LINT_VERSION): run 'make tools'"; exit 1; fi
	golangci-lint run $(PKGS)

# -race needs cgo (a C compiler); CI runs it on Linux.
test:
	$(GO) test -race -count=1 -shuffle=on $(PKGS)

tidy:
	$(GO) mod tidy
	git diff --exit-code -- go.mod go.sum

# --- WP-L1 contracts aggregate (schemas/, api/) ------------------------------
.PHONY: examples mirrors layout index clients contracts scripts-test

# Every schemas/common example both ways (offline).
examples:
	GO=$(GO) scripts/validate-examples.sh

# Every mirror re-fetched at its pinned commit (online; REQUIRE_MIRRORS=1
# turns "unverified" into a failure).
mirrors:
	GO=$(GO) scripts/check-mirrors.sh

# KT-3 skeleton layout of every pinned system (online).
layout:
	GO=$(GO) scripts/check-layout.sh

# Regenerate api/index.md from the mirrors.
index:
	$(GO) run ./scripts/contracts index

# Regenerate the Go and TypeScript clients of every pinned system.
clients:
	GO=$(GO) scripts/gen-clients.sh

# pin.sh, check-mirrors.sh and check-layout.sh against a local fixture
# repository, both ways (offline).
scripts-test:
	GO=$(GO) scripts/test-scripts.sh

# What the contracts CI job runs, offline part.
contracts: examples scripts-test
	$(GO) run ./scripts/contracts index -check

# --- WP-L5 SITL, simulators and the scenario runner (sim/, cmd/sim-*) --------
.PHONY: sim sim-down sim-venv sim-lint sim-test scenario scenarios-ci

N      ?= 1
PY     ?= python3
SIM_PY ?= sim/.venv/bin/python

# N ArduCopter SITL vehicles (Linux or WSL; home and ports from
# sim/sitl.env). sim-down says what it stopped and exits 0 when nothing
# of this fleet is left running.
sim:
	sim/run_sitl.sh -n $(N)

sim-down:
	sim/stop_sitl.sh

# The pinned Python tools for sim/ (pymavlink, ruff, mypy, pytest).
sim-venv:
	$(PY) -m venv sim/.venv
	$(SIM_PY) -m pip install -q -r sim/requirements-dev.txt

sim-lint:
	cd sim && ../$(SIM_PY) -m ruff format --check . && ../$(SIM_PY) -m ruff check . && ../$(SIM_PY) -m mypy

sim-test:
	cd sim && ../$(SIM_PY) -m pytest

# Run scenarios: make scenario TARGETS=targets/reference.yaml SCENARIOS="scenarios/sc-01-hover-inside-minima.yaml"
# VEHICLES=sitl flies SITL through sim/fly.py (start the fleet with make sim).
TARGETS   ?= targets/reference.yaml
VEHICLES  ?= synthetic
SCENARIOS ?= scenarios/kt4-baseline.yaml
scenario:
	$(GO) run ./cmd/scenario run --targets $(TARGETS) --vehicles $(VEHICLES) $(SCENARIOS)

# What the scenarios workflow runs: every reference scenario, in parallel.
scenarios-ci:
	GO=$(GO) scripts/run-reference-scenarios.sh results/ci-reference

# --- WP-L2 lab stack (deploy/) -------------------------------------------------
.PHONY: dss-up dss-down sim-ussp-up

# Start the DSS and the lab issuer, wait until healthy, prove them
# together (deploy/README.md). Needs Docker with Compose v2.
dss-up:
	deploy/dss-up.sh

# Remove the stack and its volumes; keeps deploy/local/ (key, secrets).
dss-down:
	deploy/dss-down.sh

# WP-L5: the peer USSP (profile sim) against that DSS, checked by reading
# its writes back from the DSS (deploy/README.md). make dss-down removes it.
sim-ussp-up:
	deploy/sim-ussp-up.sh

# --- WP-L6 the systems stack, the seed, the results site ----------------------
.PHONY: demo demo-down demo-seed demo-sessions results

# The DSS, the issuer and the four systems (deploy/demo-up.sh), then the
# seed through the systems' public APIs (cmd/demo-seed). The scenarios
# run after it (docs/RUNBOOKS/demo.md). Needs Docker with Compose v2.
DEMO_SCENARIOS ?= scenarios/*.yaml
DEMO_GEOID     ?= ../../_geoids/egm2008-2_5.pgm
SITL_READER    ?= bash -c "exec ~/ardupilot-venv/bin/python sim/mav_reader.py --sysid {sysid} --out udp:127.0.0.1:{out_port} --max-seconds {max_s}"
SITL_FLY       ?= bash -c "exec ~/ardupilot-venv/bin/python sim/fly.py --link udpin:127.0.0.1:{fly_port} --plan -"
demo:
	deploy/demo-up.sh
	$(GO) run ./cmd/demo-seed --steps receivers,registry,uspace,ussp,ansp,sessions --geoid $(DEMO_GEOID) \
	  --sitl-reader '$(SITL_READER)' --sitl-fly '$(SITL_FLY)' $(DEMO_SCENARIOS)

# Console sessions end after 30 minutes idle: refresh them before a run.
demo-sessions:
	$(GO) run ./cmd/demo-seed --steps sessions --geoid $(DEMO_GEOID) \
	  --sitl-reader '$(SITL_READER)' --sitl-fly '$(SITL_FLY)' $(DEMO_SCENARIOS)

# Publish the zones of named scenarios: make demo-seed STEPS=zones ZONES=sc-03-zone-entry-exit
STEPS ?= sessions
ZONES ?=
demo-seed:
	$(GO) run ./cmd/demo-seed --steps $(STEPS) --zones '$(ZONES)' --geoid $(DEMO_GEOID) \
	  --sitl-reader '$(SITL_READER)' --sitl-fly '$(SITL_FLY)' $(DEMO_SCENARIOS)

demo-down:
	deploy/demo-down.sh

# The results pages (cmd/results) into site/.
results:
	$(GO) run ./cmd/results build --in results --out site

# --- WP-L3 basemap bundle (basemap/) -------------------------------------------
# Linux, or a golang container: font-maker is compiled by basemap/tools.sh
# (cmake, clang, libfreetype-dev, libboost-dev). BUILD is a Protomaps
# daily build (YYYYMMDD); empty means the newest. See basemap/README.md.
.PHONY: basemap basemap-verify basemap-storybook
BASEMAP_OUT ?= local/basemap
BUILD       ?=

basemap:
	basemap/build.sh $(BASEMAP_OUT) $(BUILD)

basemap-verify:
	basemap/verify.sh $(BASEMAP_OUT)

basemap-storybook:
	basemap/storybook.sh local/basemap-storybook $(BUILD)

# --- WP-L7 conformance suite (conformance/) ------------------------------------
.PHONY: conformance conformance-test conformance-qualifier-check conformance-axe-test conformance-axe

# The whole suite for one target: make conformance TARGET=ansp (the
# target file conformance/targets/$(TARGET).yaml names what it needs from
# the environment; conformance/README.md). Gated on
# conformance/baseline/$(TARGET).json when it exists.
TARGET ?=
conformance:
	@if [ -z "$(TARGET)" ]; then echo "make conformance TARGET=<ansp|authority|cisp|ussp|sim-ussp>"; exit 2; fi
	GO=$(GO) conformance/run-target.sh $(TARGET)

# The suite's own tests, both ways (offline; CONFORMANCE_CONTRACTS_DIR
# adds the systems' contracts).
conformance-test:
	$(GO) test -count=1 -shuffle=on ./conformance/... ./cmd/conformance/

# Every uss_qualifier configuration validated by the pinned image (Docker).
conformance-qualifier-check:
	GO=$(GO) conformance/uss_qualifier/check.sh

# The axe runner's own Playwright test (Node, Chromium).
conformance-axe-test:
	cd conformance/axe && npm ci && npx playwright install chromium && npx playwright test tests/runner.spec.ts

# axe over CONFORMANCE_PAGES into conformance/axe/axe-results.json (informative, L-Q10).
conformance-axe:
	cd conformance/axe && npx playwright test tests/pages.spec.ts
