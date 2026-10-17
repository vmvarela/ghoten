.PHONY: build verify clean

build:
	./scripts/build.sh

verify:
	bash ./scripts/verify.sh

clean:
	rm -rf dist
