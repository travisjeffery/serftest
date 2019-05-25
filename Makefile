build:
	go build -o app main.go
	docker build -t distapp .

clean:
	rm app
