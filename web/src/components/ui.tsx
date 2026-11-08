import { Slot } from "@radix-ui/react-slot"
import * as Dialog from "@radix-ui/react-dialog"
import * as Popover from "@radix-ui/react-popover"
import * as Switch from "@radix-ui/react-switch"
import * as Label from "@radix-ui/react-label"
import * as Collapsible from "@radix-ui/react-collapsible"
import * as ToggleGroup from "@radix-ui/react-toggle-group"
import { Command as Cmd } from "cmdk"
import { cva, type VariantProps } from "class-variance-authority"
import { X } from "lucide-react"
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
