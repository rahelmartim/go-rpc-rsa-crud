// Package catalog keeps the products of the server in memory.
package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Product is one item of the catalog. The price is in cents, so that no
// money is lost to floating point.
type Product struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price int64  `json:"price"`
}

// MaxProducts limits the size of the catalog.
const MaxProducts = 1000

var (
	ErrNotFound = errors.New("no product has this id")
	ErrBadName  = errors.New("a name has 1 to 40 letters, digits, spaces, dots, dashes, or underscores")
	ErrBadPrice = errors.New("a price is a number with at most 2 decimals, such as 27 or 4.50")
	ErrFull     = fmt.Errorf("the catalog is full: at most %d products", MaxProducts)
)

var (
	nameRe  = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,39}$`)
	priceRe = regexp.MustCompile(`^([0-9]{1,9})(?:[.,]([0-9]{1,2}))?$`)
)

// ParseName checks a product name and trims its spaces.
func ParseName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !nameRe.MatchString(s) {
		return "", ErrBadName
	}
	return s, nil
}

// ParsePrice reads a price such as "27", "4.5", or "4,50" as cents.
func ParsePrice(s string) (int64, error) {
	m := priceRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, ErrBadPrice
	}
	units, _ := strconv.ParseInt(m[1], 10, 64)
	var cents int64
	if m[2] != "" {
		frac := m[2]
		if len(frac) == 1 {
			frac += "0"
		}
		cents, _ = strconv.ParseInt(frac, 10, 64)
	}
	return units*100 + cents, nil
}

// FormatPrice writes cents as a price, such as "4.50".
func FormatPrice(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

// Catalog is safe for use by many connections at the same time.
type Catalog struct {
	mu    sync.Mutex
	items map[int]Product
	next  int
}

func New() *Catalog {
	return &Catalog{items: map[int]Product{}, next: 1}
}

func parse(name, price string) (string, int64, error) {
	n, err := ParseName(name)
	if err != nil {
		return "", 0, err
	}
	p, err := ParsePrice(price)
	if err != nil {
		return "", 0, err
	}
	return n, p, nil
}

func (c *Catalog) Create(name, price string) (Product, error) {
	n, p, err := parse(name, price)
	if err != nil {
		return Product{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= MaxProducts {
		return Product{}, ErrFull
	}
	product := Product{ID: c.next, Name: n, Price: p}
	c.items[product.ID] = product
	c.next++
	return product, nil
}

func (c *Catalog) Get(id int) (Product, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	product, ok := c.items[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	return product, nil
}

// Update changes a product that exists. It does not create one.
func (c *Catalog) Update(id int, name, price string) (Product, error) {
	n, p, err := parse(name, price)
	if err != nil {
		return Product{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[id]; !ok {
		return Product{}, ErrNotFound
	}
	product := Product{ID: id, Name: n, Price: p}
	c.items[id] = product
	return product, nil
}

func (c *Catalog) Delete(id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[id]; !ok {
		return ErrNotFound
	}
	delete(c.items, id)
	return nil
}

// List returns the products in the order of their ids.
func (c *Catalog) List() []Product {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := make([]Product, 0, len(c.items))
	for _, p := range c.items {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}
