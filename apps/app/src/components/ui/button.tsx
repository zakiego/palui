/**
 * The button, on ui-sdk's Button, styled for this dashboard.
 *
 * Linear's button: a 32px pill with a 13px label at weight 510, on a phone
 * too. Every variant presses to 0.97 on a transition, so a release mid-press
 * eases back instead of snapping. `static` turns that off where motion would
 * distract. Focus is not styled here: one unlayered rule in index.css draws
 * the ring for every control, so no variant can forget it.
 */

import * as React from "react"
import { Button as ButtonPrimitive } from "@ui-sdk/react/button"
import { renderElement } from "@ui-sdk/react/primitive"
import { cva, type VariantProps } from "class-variance-authority"

import { cn, cnState } from "@/lib/utils"

const buttonVariants = cva(
  "group/button inline-flex shrink-0 cursor-pointer items-center justify-center rounded-full border border-transparent bg-clip-padding text-caption font-medium whitespace-nowrap transition-[scale,background-color,color,box-shadow,opacity] duration-150 ease-out outline-none select-none disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default:
          "bg-primary text-primary-foreground shadow-[0_1px_1px_rgb(0_0_0/0.12)] hover:bg-primary-hover",
        outline:
          "bg-card text-foreground shadow-(--shadow-button) hover:bg-accent aria-expanded:bg-accent dark:bg-white/5 dark:hover:bg-(--secondary-hover) dark:aria-expanded:bg-(--secondary-hover)",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-accent aria-expanded:bg-accent",
        ghost:
          "text-muted-foreground hover:bg-accent hover:text-foreground aria-expanded:bg-accent aria-expanded:text-foreground",
        destructive:
          "bg-destructive-bg text-destructive-fg shadow-[0_0_0_1px_var(--destructive-border)] hover:bg-[color-mix(in_oklch,var(--destructive-bg),var(--destructive)_12%)]",
        danger:
          "bg-destructive-solid text-white shadow-[0_1px_2px_oklch(0_0_0/0.12)] hover:bg-destructive-solid-hover",
        "destructive-ghost":
          "text-destructive-fg hover:bg-destructive-bg",
        // Links skip the press scale (it would move the text), so they dim instead.
        link: "h-auto! px-0! text-primary-fg underline decoration-from-font underline-offset-[3px] [text-underline-position:from-font] hover:decoration-2 active:opacity-70",
      },
      size: {
        default:
          "h-8 gap-1.5 px-3 has-data-[icon=inline-end]:pe-2.5 has-data-[icon=inline-start]:ps-2.5",
        xs: "h-6 gap-1 px-2 text-xs [&_svg:not([class*='size-'])]:size-3",
        // Inline links in caption text, with taller tap room on a touch screen.
        caption: "gap-1 text-caption pointer-coarse:py-2 [&_svg:not([class*='size-'])]:size-3.5",
        sm: "h-7 gap-1.5 px-2.5 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 gap-2 px-4",
        icon: "size-8",
        "icon-xs": "size-6 [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-7",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

type ButtonVariant = VariantProps<typeof buttonVariants>

function buttonClass(
  variant: ButtonVariant["variant"],
  size: ButtonVariant["size"],
  isStatic: boolean
) {
  return cn(
    buttonVariants({ variant, size }),
    !isStatic && variant !== "link" && "active:not-disabled:scale-[0.97]"
  )
}

function Button({
  className,
  variant = "default",
  size = "default",
  static: isStatic = false,
  ...props
}: React.ComponentProps<typeof ButtonPrimitive> &
  VariantProps<typeof buttonVariants> & { static?: boolean }) {
  return (
    <ButtonPrimitive
      className={cnState(buttonClass(variant, size, isStatic), className)}
      {...props}
    />
  )
}

type ButtonProps = React.ComponentProps<typeof Button>

// A trigger's render={<Button />} becomes a plain styled button: the trigger
// already owns the behavior, and a second ui-sdk Button on the same element
// would show up twice to agents.
function partRender<R>(render: R): R {
  if (!React.isValidElement<ButtonProps>(render) || render.type !== Button) return render
  const { className, variant = "default", size = "default", static: isStatic = false, render: inner, ...rest } =
    render.props
  return (
    <PlainButton
      {...(rest as React.ComponentProps<"button">)}
      inner={inner}
      className={cn(buttonClass(variant, size, isStatic), className as string | undefined)}
    />
  ) as R
}

function PlainButton({
  inner,
  ...props
}: React.ComponentProps<"button"> & { inner?: ButtonProps["render"] }) {
  return renderElement("button", { type: "button", ...props }, inner as Parameters<typeof renderElement>[2])
}

export { Button, buttonVariants, partRender }
