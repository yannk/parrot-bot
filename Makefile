all:
	for D in `go list -f "{{range .Imports}}{{ . }} {{end}}" parrot.go`; do go get $$D; done
	go build -o parrot parrot.go

clean:
	rm parrot
