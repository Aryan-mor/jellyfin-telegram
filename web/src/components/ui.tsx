import { Slot } from "@radix-ui/react-slot"
import * as Dialog from "@radix-ui/react-dialog"
import * as Popover from "@radix-ui/react-popover"
import * as Switch from "@radix-ui/react-switch"
import * as Label from "@radix-ui/react-label"
import * as Collapsible from "@radix-ui/react-collapsible"
import * as ToggleGroup from "@radix-ui/react-toggle-group"
import * as SelectPrimitive from "@radix-ui/react-select"
import * as CheckboxPrimitive from "@radix-ui/react-checkbox"
import { Command as Cmd } from "cmdk"
import { cva, type VariantProps } from "class-variance-authority"
import { Check, ChevronDown, Info, TriangleAlert, X } from "lucide-react"
import { type ButtonHTMLAttributes, type ComponentProps, type ReactNode } from "react"
import { cn } from "@/lib/utils"

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50 outline-none focus-visible:ring-2 focus-visible:ring-ring",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        secondary: "bg-secondary text-secondary-foreground hover:bg-secondary/80",
        ghost: "hover:bg-accent",
        destructive: "bg-destructive text-white hover:bg-destructive/90",
        outline: "border bg-card hover:bg-accent",
      },
      size: {
        default: "h-9 px-3",
        sm: "h-8 px-2.5 text-xs",
        icon: "size-8",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
)

export function Button({ className, variant, size, asChild, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & VariantProps<typeof buttonVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "button"
  return <Comp className={cn(buttonVariants({ variant, size }), className)} {...props} />
}

export function Input(props: ComponentProps<"input">) {
  return <input {...props} className={cn("flex h-9 w-full rounded-md border bg-card px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring", props.className)} />
}

export function Textarea(props: ComponentProps<"textarea">) {
  return <textarea {...props} className={cn("flex min-h-20 w-full rounded-md border bg-card px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring", props.className)} />
}

export function FieldLabel(props: ComponentProps<typeof Label.Root>) {
  return <Label.Root {...props} className={cn("text-sm font-medium", props.className)} />
}

export function Card({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("rounded-lg border bg-card text-card-foreground shadow-sm", className)} {...props} />
}

export function CardBody({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("p-4", className)} {...props} />
}

export function SwitchBox({ checked, onCheckedChange, label }: { checked: boolean; onCheckedChange: (v: boolean) => void; label: string }) {
  return (
    <Switch.Root checked={checked} onCheckedChange={onCheckedChange} aria-label={label} className="relative h-5 w-9 shrink-0 rounded-full bg-input data-[state=checked]:bg-primary">
      <Switch.Thumb className="block size-4 translate-x-0.5 rounded-full bg-white transition-transform data-[state=checked]:translate-x-4" />
    </Switch.Root>
  )
}

export function DialogRoot(props: ComponentProps<typeof Dialog.Root>) { return <Dialog.Root {...props} /> }
export function DialogTrigger(props: ComponentProps<typeof Dialog.Trigger>) { return <Dialog.Trigger {...props} /> }

export function DialogContent({ title, children, className }: { title: string; children: ReactNode; className?: string }) {
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
      <Dialog.Content className={cn("fixed left-1/2 top-1/2 z-50 w-[min(32rem,calc(100%-2rem))] -translate-x-1/2 -translate-y-1/2 rounded-lg border bg-card p-4 shadow-lg", className)}>
        <div className="mb-3 flex items-start justify-between gap-3">
          <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
          <Dialog.Close className="rounded-md p-1 hover:bg-accent" aria-label="Close"><X className="size-4" /></Dialog.Close>
        </div>
        {children}
      </Dialog.Content>
    </Dialog.Portal>
  )
}

export function Sheet({ open, onOpenChange, children }: { open: boolean; onOpenChange: (v: boolean) => void; children: ReactNode }) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40 md:hidden" />
        <Dialog.Content className="fixed inset-y-0 left-0 z-50 w-64 border-r bg-card p-3 shadow-lg md:hidden">
          <Dialog.Title className="sr-only">Menu</Dialog.Title>
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

export function PopoverRoot(props: ComponentProps<typeof Popover.Root>) { return <Popover.Root {...props} /> }
export const PopoverTrigger = Popover.Trigger

export function PopoverContent({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <Popover.Portal>
      <Popover.Content className={cn("z-50 w-72 rounded-md border bg-popover p-1 text-popover-foreground shadow-md", className)} sideOffset={6}>
        {children}
      </Popover.Content>
    </Popover.Portal>
  )
}

export function Command({ children }: { children: ReactNode }) {
  return <Cmd className="flex flex-col">{children}</Cmd>
}
export function CommandInput(props: ComponentProps<typeof Cmd.Input>) {
  return <Cmd.Input {...props} className={cn("h-8 w-full bg-transparent px-2 text-sm outline-none", props.className)} />
}
export function CommandList({ children }: { children: ReactNode }) {
  return <Cmd.List className="max-h-64 overflow-auto">{children}</Cmd.List>
}
export function CommandEmpty({ children }: { children: ReactNode }) {
  return <Cmd.Empty className="px-2 py-3 text-sm text-muted-foreground">{children}</Cmd.Empty>
}
export function CommandItem({ children, onSelect, value }: { children: ReactNode; onSelect: () => void; value: string }) {
  return <Cmd.Item value={value} onSelect={onSelect} className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm aria-selected:bg-accent">{children}</Cmd.Item>
}

export function Collapse({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Collapsible.Root>
      <Collapsible.Trigger className="text-sm font-medium underline-offset-2 hover:underline">{title}</Collapsible.Trigger>
      <Collapsible.Content className="pt-2 text-sm text-muted-foreground">{children}</Collapsible.Content>
    </Collapsible.Root>
  )
}

export function Toggles({ value, onChange, options }: { value: string; onChange: (v: string) => void; options: [string, string][] }) {
  return (
    <ToggleGroup.Root type="single" value={value} onValueChange={(v) => v && onChange(v)} className="flex flex-wrap gap-1">
      {options.map(([id, label]) => (
        <ToggleGroup.Item key={id} value={id} className="h-8 rounded-md border px-2.5 text-xs data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">{label}</ToggleGroup.Item>
      ))}
    </ToggleGroup.Root>
  )
}

export function Select({ value, onValueChange, options, placeholder, className, size = "default", label }: {
  value: string
  onValueChange: (v: string) => void
  options: [string, string][]
  placeholder?: string
  className?: string
  size?: "default" | "sm"
  label?: string
}) {
  return (
    <SelectPrimitive.Root value={value || undefined} onValueChange={onValueChange}>
      <SelectPrimitive.Trigger aria-label={label} className={cn("flex w-full items-center justify-between gap-2 rounded-md border bg-card px-3 text-left text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring data-[placeholder]:text-muted-foreground", size === "sm" ? "h-8 text-xs" : "h-9", className)}>
        <span className="truncate"><SelectPrimitive.Value placeholder={placeholder} /></span>
        <SelectPrimitive.Icon><ChevronDown className="size-4 opacity-60" /></SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content position="popper" sideOffset={4} className="z-50 max-h-72 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md">
          <SelectPrimitive.Viewport className="p-1">
            {options.map(([v, text]) => (
              <SelectPrimitive.Item key={v} value={v} className="relative flex h-8 cursor-pointer select-none items-center rounded-sm pl-7 pr-2 text-sm outline-none data-[highlighted]:bg-accent">
                <SelectPrimitive.ItemIndicator className="absolute left-2"><Check className="size-3.5" /></SelectPrimitive.ItemIndicator>
                <SelectPrimitive.ItemText>{text}</SelectPrimitive.ItemText>
              </SelectPrimitive.Item>
            ))}
          </SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  )
}

export function Checkbox({ checked, onCheckedChange, label, disabled }: { checked: boolean; onCheckedChange: (v: boolean) => void; label?: ReactNode; disabled?: boolean }) {
  const box = (
    <CheckboxPrimitive.Root checked={checked} disabled={disabled} onCheckedChange={(v) => onCheckedChange(v === true)} aria-label={typeof label === "string" ? label : undefined}
      className="flex size-4 shrink-0 items-center justify-center rounded-[4px] border border-input bg-card outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground">
      <CheckboxPrimitive.Indicator><Check className="size-3" /></CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
  if (label === undefined) return box
  return <label className="inline-flex cursor-pointer items-center gap-2 text-sm">{box}<span>{label}</span></label>
}

export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return <div className="w-full overflow-x-auto"><table className={cn("w-full caption-bottom text-sm", className)}>{children}</table></div>
}
export function THead({ children }: { children: ReactNode }) {
  return <thead className="[&_tr]:border-b">{children}</thead>
}
export function TBody({ children }: { children: ReactNode }) {
  return <tbody className="[&_tr:last-child]:border-0">{children}</tbody>
}
export function TR({ children, className }: { children: ReactNode; className?: string }) {
  return <tr className={cn("border-b align-middle", className)}>{children}</tr>
}
export function TH({ children, className }: { children?: ReactNode; className?: string }) {
  return <th className={cn("h-9 px-2 text-left text-xs font-medium text-muted-foreground", className)}>{children}</th>
}
export function TD({ children, className }: { children?: ReactNode; className?: string }) {
  return <td className={cn("px-2 py-2", className)}>{children}</td>
}

export function Alert({ tone = "info", children, action }: { tone?: "info" | "warn" | "bad"; children: ReactNode; action?: ReactNode }) {
  const Icon = tone === "info" ? Info : TriangleAlert
  return (
    <div role={tone === "bad" ? "alert" : "status"} className={cn("flex items-start gap-2 rounded-md border px-3 py-2 text-sm",
      tone === "bad" && "border-destructive/40 bg-destructive/10 text-destructive",
      tone === "warn" && "border-amber-500/40 bg-amber-500/10")}>
      <Icon className="mt-0.5 size-4 shrink-0" />
      <div className="min-w-0 flex-1">{children}</div>
      {action}
    </div>
  )
}

export function Progress({ value }: { value: number }) {
  return <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted"><div className="h-full bg-primary transition-[width]" style={{ width: Math.max(0, Math.min(100, value)) + "%" }} /></div>
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="py-3 text-center text-sm text-muted-foreground">{children}</p>
}

// Disclosure is a section that opens and closes. Its body is only built while it is open, so a
// library of thousands of episodes stays quick. Buttons in actions do not toggle the section.
export function Disclosure({ title, meta, actions, defaultOpen, children, level = 1 }: {
  title: ReactNode
  meta?: ReactNode
  actions?: ReactNode
  defaultOpen?: boolean
  children: ReactNode
  level?: 1 | 2
}) {
  return (
    <Collapsible.Root defaultOpen={defaultOpen} className={cn("group/d", level === 1 ? "rounded-md border" : "border-t first:border-t-0")}>
      <div className={cn("flex items-center gap-2", level === 1 ? "px-3 py-2" : "py-2 pl-2 pr-1")}>
        <Collapsible.Trigger className="flex min-w-0 flex-1 items-center gap-2 text-left">
          <ChevronDown className="size-4 shrink-0 -rotate-90 opacity-60 transition-transform group-data-[state=open]/d:rotate-0" />
          <span className={cn("truncate", level === 1 ? "font-medium" : "text-sm")}>{title}</span>
          {meta && <span className="truncate text-xs text-muted-foreground">{meta}</span>}
        </Collapsible.Trigger>
        {actions && <div className="flex shrink-0 gap-1">{actions}</div>}
      </div>
      <Collapsible.Content className={cn(level === 1 ? "border-t px-3 pb-2" : "pl-6")}>{children}</Collapsible.Content>
    </Collapsible.Root>
  )
}
