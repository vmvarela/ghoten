.PHONY: patch build verify clean

patch:
	./scripts/generate-patch.sh

build:
	./scripts/build.sh

verify: patch build

clean:
	rm -rf dist
	rm -f patches/ghoten.patch
