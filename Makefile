NAME := shorturls

default: run

run:
	@templ generate
	@go run .
