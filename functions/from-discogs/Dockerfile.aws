FROM golang:1.21 as build
WORKDIR /app
COPY go.mod go.sum ./
COPY *.go .
RUN GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o main

# Copy artifacts to a clean image
FROM public.ecr.aws/lambda/provided:al2
COPY --from=build /app/main ./main
ENTRYPOINT [ "./main" ]
