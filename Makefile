air: 
	air --build.cmd "go run ./cmd/api/" --build.exclude_dir "var"

app: 
	go build -o ./tmp/api/ ./cmd/api/ && docker compose down app && docker compose up app -d
