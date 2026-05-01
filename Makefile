# idlc-go — friendly entry points for build, test, and corpus management.
#
# The Core3 corpus and idlc.jar are not redistributable, so they are
# .gitignored and pulled locally via `make pull-core3` + `make fetch-corpus`.
# Tests that need them auto-skip when missing (`internal/corpus` package).
#
# Override the Core3 location: `make CORE3_PATH=/abs/path test-corpus`.

CORE3_PATH ?= submodules/Core3
JAR        := $(CORE3_PATH)/MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar
ENGINE3_SRC := $(CORE3_PATH)/MMOCoreORB/utils/engine3/MMOEngine/src

.PHONY: help build test test-corpus test-probes test-all \
        fmt vet pull-core3 fetch-corpus regen-probes regen-oracle \
        ensure-core3 clean-got \
        baseline-jar baseline-idlc-go baseline-diff baseline

help:
	@echo "idlc-go — common targets"
	@echo ""
	@echo "  make build           Build the idlc-go binary"
	@echo "  make test            Run all tests (corpus tests skip if Core3 absent)"
	@echo "  make test-probes     Run only the probe tests"
	@echo "  make test-corpus     Run only the corpus tests (requires Core3)"
	@echo "  make test-all        Run everything; fail if Core3 isn't present"
	@echo "  make fmt             gofmt -w on the tree"
	@echo "  make vet             go vet ./..."
	@echo ""
	@echo "Corpus management (requires Core3):"
	@echo "  make pull-core3      git submodule update --init --recursive submodules/Core3"
	@echo "  make fetch-corpus    Copy IDLs + run JAR → testdata/idl/ + testdata/autogen/"
	@echo "  make regen-probes    Re-run JAR over testdata/probe/src/ → expected/"
	@echo "  make regen-oracle    Re-run JAR over scripts/oracle/src/ → hash test oracle"
	@echo ""
	@echo "Full-corpus baseline (requires Core3):"
	@echo "  make baseline-jar      Run JAR over every Core3 IDL → _baseline/jar/"
	@echo "  make baseline-idlc-go  Run idlc-go over every Core3 IDL → _baseline/idlc-go/"
	@echo "  make baseline          Both of the above"
	@echo "  make baseline-diff     Diff _baseline/jar vs _baseline/idlc-go, report"
	@echo ""
	@echo "Misc:"
	@echo "  make clean-got       Remove stale *.got files dropped by failed tests"
	@echo ""
	@echo "Override Core3 location: make CORE3_PATH=/abs/path <target>"

build:
	go build ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

test-probes:
	go test ./internal/probe/...

test-corpus: ensure-core3
	go test ./internal/golden/... ./internal/parser/... ./internal/sema/...

test-all: ensure-core3 test

ensure-core3:
	@test -f "$(JAR)" || { \
		echo "Core3 not present (looked for $(JAR))."; \
		echo "Run \`make pull-core3\` or set CORE3_PATH=/abs/path."; \
		exit 1; \
	}
	@test -d "$(ENGINE3_SRC)" || { \
		echo "engine3 not present (looked for $(ENGINE3_SRC))."; \
		echo "Run \`make pull-core3\` (recursive submodule pull)."; \
		exit 1; \
	}

pull-core3:
	git submodule update --init --recursive submodules/Core3

fetch-corpus: ensure-core3
	CORE3_PATH=$(CORE3_PATH) bash scripts/fetch-corpus-from-core3.sh

regen-probes: ensure-core3
	CORE3_PATH=$(CORE3_PATH) bash scripts/gen-probe-goldens.sh

regen-oracle: ensure-core3
	CORE3_PATH=$(CORE3_PATH) bash scripts/gen-oracle.sh

clean-got:
	find testdata -name '*.got' -delete

baseline-jar: ensure-core3
	CORE3_PATH=$(CORE3_PATH) bash scripts/baseline-jar.sh

baseline-idlc-go: ensure-core3
	CORE3_PATH=$(CORE3_PATH) bash scripts/baseline-idlc-go.sh

baseline: baseline-jar baseline-idlc-go

baseline-diff:
	bash scripts/baseline-diff.sh
