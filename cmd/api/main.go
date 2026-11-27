// Command api is the single FausCrew binary: serve (default), worker and
// migrate. Everything it runs is built in internal/bootstrap.
package main

import (
	"fmt"
	"log"
	"os"

	"houseflowApi/internal/bootstrap"
)

// @title HouseFlow API
// @version 1.0
// @description HouseFlow API Documentation
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.email support@houseflow.com

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:3162
// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.
func main() {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	var err error
	switch command {
	case "serve":
		err = bootstrap.Serve()
	case "worker":
		err = bootstrap.Work()
	case "migrate":
		err = bootstrap.Migrate()
	default:
		err = fmt.Errorf("unknown command %q; expected serve, worker or migrate", command)
	}

	if err != nil {
		log.Printf("application stopped with error: %v", err)
		os.Exit(1)
	}
}
