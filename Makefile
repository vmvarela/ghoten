.PHONY: build verify e2e-oras clean

build:
	./scripts/build.sh

verify:
	bash ./scripts/verify.sh

e2e-oras: build
	bash ./scripts/e2e-oras.sh

clean:
	rm -rf dist
