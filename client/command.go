package client

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
)

// Help lists the commands of Exec.
const Help = `.login <user> <password>     log in (the message is encrypted with RSA)
.create <name> <price>        add a product, such as .create rice 4.50
.read <id>                    show one product
.update <id> <name> <price>   change a product
.delete <id>                  remove a product
.list                         show all products
.logout                       end the login
.help                         show this list
.quit                         close the connection`

// ErrQuit is the result of the .quit command.
var ErrQuit = errors.New("quit")

// Describe writes a product on one line.
func Describe(p catalog.Product) string {
	return fmt.Sprintf("#%d  %s  %s", p.ID, p.Name, catalog.FormatPrice(p.Price))
}

// Exec runs one command line, such as ".create rice 4.50". Names cannot
// have spaces here: use rice_bag instead.
func (c *Client) Exec(line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil
	}
	cmd, args := strings.ToLower(fields[0]), fields[1:]
	if !strings.HasPrefix(cmd, ".") {
		return "", errors.New("commands start with a dot, such as .list. Type .help for the list")
	}
	usage := map[string]string{
		".login": ".login <user> <password>", ".create": ".create <name> <price>", ".read": ".read <id>",
		".update": ".update <id> <name> <price>", ".delete": ".delete <id>",
		".list": ".list", ".logout": ".logout", ".help": ".help", ".quit": ".quit",
	}
	want := map[string]int{".login": 2, ".create": 2, ".read": 1, ".update": 3, ".delete": 1}
	u, ok := usage[cmd]
	if !ok {
		return "", fmt.Errorf("%s is not a command. Type .help for the list", cmd)
	}
	if len(args) != want[cmd] {
		return "", fmt.Errorf("usage: %s", u)
	}
	id := 0
	if cmd == ".read" || cmd == ".update" || cmd == ".delete" {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			return "", errors.New("the id is a whole number, such as 1")
		}
		id = n
	}

	switch cmd {
	case ".help":
		return Help, nil
	case ".quit":
		return "bye", ErrQuit
	case ".login":
		if err := c.Login(args[0], args[1]); err != nil {
			return "", err
		}
		return "logged in as " + args[0], nil
	case ".logout":
		return "logged out", c.Logout()
	case ".create":
		p, err := c.Create(args[0], args[1])
		if err != nil {
			return "", err
		}
		return "created " + Describe(p), nil
	case ".read":
		p, err := c.Read(id)
		if err != nil {
			return "", err
		}
		return Describe(p), nil
	case ".update":
		p, err := c.Update(id, args[1], args[2])
		if err != nil {
			return "", err
		}
		return "updated " + Describe(p), nil
	case ".delete":
		if err := c.Delete(id); err != nil {
			return "", err
		}
		return fmt.Sprintf("deleted #%d", id), nil
	default: // .list
		products, err := c.List()
		if err != nil {
			return "", err
		}
		if len(products) == 0 {
			return "the catalog is empty", nil
		}
		lines := make([]string, len(products))
		for i, p := range products {
			lines[i] = Describe(p)
		}
		return strings.Join(lines, "\n"), nil
	}
}
