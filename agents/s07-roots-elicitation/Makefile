.PHONY: build test demo clean

build:
	go build -o s07-roots-elicitation .

test:
	go test ./...

# The demo runs the test that exercises both an accepted elicitation
# (TestFormElicitationAcceptReturnsContent + TestRootsListRoundTrip) and
# a declined one (TestDeclinedElicitationPropagates). Because elicitation
# is a server-initiated request, a stdio echo demo would deadlock — the
# in-process test harness drives both sides.
demo:
	go test -v -run 'TestRootsListRoundTrip|TestDeclinedElicitationPropagates' ./...

clean:
	rm -f s07-roots-elicitation
