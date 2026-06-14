# Code Style

## Go

### Spacing and Braces

Every block delimited by `{...}` gets a blank line after the opening brace and before the closing brace. This applies universally: function bodies, method bodies, struct declarations, `if`/`for`/`switch` blocks, composite literals, and anonymous function literals.

```go
func (s *AuthService) Login(ctx context.Context, email, password string) (*User, string, error) {

    email = strings.ToLower(strings.TrimSpace(email))

    var user User

    err := s.db.Users().FindOne(ctx, bson.M{"email": email}).Decode(&user)

    if err != nil {

        return nil, "", ErrInvalidCredentials

    }

    token, err := s.issueToken(user)

    return &user, token, err

}
```

The only exceptions are `import (...)`, `var (...)`, and `const (...)` groups — parenthesised groups do not get inner blank lines.

### Error Handling

Use early returns (guard clauses). Each `if err != nil` block follows the same blank-line pattern as any other block:

```go
result, err := doSomething()

if err != nil {

    return nil, err

}
```

### Struct Declarations

Struct fields are not horizontally aligned. No extra spaces to align types, tags, or values across lines:

```go
// Correct
type User struct {

    ID string
    IsAdmin bool

    Email string
    
    CreatedAt time.Time

}

// Wrong — do not align
type User struct {
    ID        string
    Email     string
    IsAdmin   bool
    CreatedAt time.Time
}
```

### Var and Const Groups

No alignment of `=` signs across lines in `var` or `const` groups:

```go
// Correct
var (
    ErrNotFound = errors.New("not found")
    ErrUnauthorized = errors.New("unauthorized")
    ErrRateLimited = errors.New("rate limited")
)
```

### Imports

Group imports in this order, each group separated by a blank line:

1. Standard library
2. Internal packages (`streamly/...`, `mediakit/...`)
3. External packages (`github.com/...`, `go.mongodb.org/...`)

### Composite Literals

Struct and map literals follow the same blank-line rule as blocks. Related fields should be grouped together with blank lines between groups, but no extra spaces to align field names or values:

```go
user := models.User{

    Email: email,
    PasswordHash: hash,
    
    CreatedAt: now,
    
    IsAdmin: false,

}
```

### Comments

Add comments only when the *why* is non-obvious - a hidden constraint, a workaround, or surprising behavior. Never describe what the code does (well-named identifiers do that). One short line maximum; no multi-line comment blocks.

### General Rules

- No trailing whitespace on any line
- Consistent tab indentation (gofmt standard)
- Blank lines between distinct statement groups within a function body
- Short one-liner functions (body on same line as `{`) are acceptable only when the entire function fits on a single line

---

## TypeScript / TSX

### Spacing and Braces

The same blank-line-inside-braces rule applies to TypeScript class bodies, method bodies, interface declarations, and all control flow blocks:

```tsx
interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {

  variant?: "default" | "ghost" | "outline";
  size?: "sm" | "md" | "lg";
  children: ReactNode;

}

export class Button extends Component<ButtonProps> {

  render() {

    const { className, variant = "default", children, ...props } = this.props;

    if (!children) {

      return null;

    }

    return (

      <button className={cn(className)} {...props}>

        {children}

      </button>

    );

  }

}
```

### Imports

Group imports with blank lines between groups:

1. External libraries (`react`, `lucide-react`, etc.)
2. Internal components and pages (`@/components/...`, `@/pages/...`)
3. Internal utilities and types (`@/lib/...`)

### Interface Props

Do not horizontally align property types or default values:

```tsx
// Correct
interface VideoPlayerProps {

  src: string;
  isHls: boolean;
  title: string;
  poster?: string;

}
```

### JSX Formatting

Multi-line JSX expressions follow the same blank-line pattern — blank line after the opening tag and before the closing tag when the content spans multiple lines. Prop lists that span multiple lines each get their own line, indented one level.

### Comments

Same rule as Go: only when the why is non-obvious. No JSDoc unless documenting a public API surface.

### General Rules

- No trailing whitespace
- Consistent 2-space indentation
- Prefer class components when state or lifecycle is needed (this codebase uses class components throughout)
- Arrow functions for event handlers and callbacks assigned to class fields
