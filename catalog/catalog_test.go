package catalog

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestCreateReadUpdateDelete(t *testing.T) {
	c := New()
	rice, err := c.Create("rice", "4.50")
	if err != nil {
		t.Fatal(err)
	}
	if rice.ID != 1 || rice.Name != "rice" || rice.Price != 450 {
		t.Fatalf("created %+v", rice)
	}
	coffee, _ := c.Create("  coffee beans ", "12,9")
	if coffee.ID != 2 || coffee.Name != "coffee beans" || coffee.Price != 1290 {
		t.Fatalf("created %+v", coffee)
	}

	got, err := c.Get(1)
	if err != nil || got != rice {
		t.Fatalf("Get(1) = %+v, %v", got, err)
	}
	updated, err := c.Update(1, "brown rice", "5")
	if err != nil || updated.Price != 500 || updated.Name != "brown rice" {
		t.Fatalf("Update = %+v, %v", updated, err)
	}
	if err := c.Delete(1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: %v", err)
	}
	if list := c.List(); len(list) != 1 || list[0].ID != 2 {
		t.Fatalf("List = %+v", list)
	}
	// Ids are not used again after a delete.
	if p, _ := c.Create("tea", "3"); p.ID != 3 {
		t.Fatalf("the next id is %d, want 3", p.ID)
	}
}

func TestMissingProducts(t *testing.T) {
	c := New()
	if _, err := c.Update(9, "tea", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update of a missing product: %v", err)
	}
	if err := c.Delete(9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete of a missing product: %v", err)
	}
	if len(c.List()) != 0 {
		t.Fatal("Update must not create a product")
	}
}

func TestValidation(t *testing.T) {
	c := New()
	for _, name := range []string{"", "   ", "-rice", "rice;drop", "<b>", strings.Repeat("a", 41)} {
		if _, err := c.Create(name, "1"); !errors.Is(err, ErrBadName) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for _, price := range []string{"", "abc", "-1", "1.234", "1e5", "9999999999", "1.2.3"} {
		if _, err := c.Create("tea", price); !errors.Is(err, ErrBadPrice) {
			t.Errorf("price %q: %v", price, err)
		}
	}
	if _, err := c.Create("café_crème-2.0", "0"); err != nil {
		t.Errorf("a valid name was refused: %v", err)
	}
}

func TestPrices(t *testing.T) {
	cases := map[string]int64{"0": 0, "27": 2700, "4.5": 450, "4,50": 450, "0.05": 5, "999999999.99": 99999999999}
	for in, want := range cases {
		got, err := ParsePrice(in)
		if err != nil || got != want {
			t.Errorf("ParsePrice(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for cents, want := range map[int64]string{0: "0.00", 5: "0.05", 450: "4.50", 2700: "27.00"} {
		if got := FormatPrice(cents); got != want {
			t.Errorf("FormatPrice(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestFull(t *testing.T) {
	c := New()
	for i := range MaxProducts {
		if _, err := c.Create(fmt.Sprintf("p%d", i), "1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Create("one more", "1"); !errors.Is(err, ErrFull) {
		t.Fatalf("got %v, want ErrFull", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				p, err := c.Create(fmt.Sprintf("w%d-%d", w, i), "1")
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = c.Update(p.ID, p.Name, "2")
				_ = c.List()
			}
		}()
	}
	wg.Wait()
	list := c.List()
	if len(list) != 200 {
		t.Fatalf("%d products, want 200", len(list))
	}
	for i, p := range list {
		if p.ID != i+1 {
			t.Fatalf("ids are not unique and in order: %d at %d", p.ID, i)
		}
	}
}
