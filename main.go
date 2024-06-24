package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/bitcoin-sv/go-sdk/script"
	"github.com/bitcoin-sv/go-sdk/transaction/template/p2pkh"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"github.com/shruggr/go-opns-mint/opns"
)

func init() {
	godotenv.Load(".env")
}

type MineRequest struct {
	Domain         string `json:"domain"`
	OwnerAddress   string `json:"ownerAddress"`
	FundingAddress string `json:"fundingAddress"`
}

func main() {
	app := fiber.New()

	// Define a route for the GET method on the root path '/'
	app.Post("/mine", func(c *fiber.Ctx) error {
		req := &MineRequest{}
		if err := c.BodyParser(req); err != nil {
			log.Println("BodyParser", err)
			return c.Status(http.StatusBadRequest).SendString(err.Error())
		}
		ownerAdd, err := script.NewAddressFromString(req.OwnerAddress)
		if err != nil {
			log.Println("NewAddressFromString", err)
			return c.Status(http.StatusBadRequest).SendString(err.Error())
		}
		if ownerScript, err := p2pkh.Lock(ownerAdd); err != nil {
			log.Println("Lock", err)
			return c.Status(http.StatusBadRequest).SendString(err.Error())
		} else if txid, err := opns.MineDomain(c.Context(), req.Domain, ownerScript); err != nil {
			log.Println("MineDomain", err)
			return c.Status(http.StatusInternalServerError).SendString(err.Error())
		} else {
			return c.SendString(txid)
		}
	})

	app.Get("/refresh", func(c *fiber.Ctx) error {
		if balance, err := opns.RefreshBalance(c.Context()); err != nil {
			log.Println("RefreshBalance", err)
			return c.Status(http.StatusInternalServerError).SendString(err.Error())
		} else {
			return c.JSON(balance)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	// Start the server
	log.Fatal(app.Listen(fmt.Sprintf(":%s", port)))

}
