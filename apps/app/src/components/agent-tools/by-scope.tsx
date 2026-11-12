// The Tools tab list, in two parts picked with big cards: the servers only
// this agent has, and the global ones every agent shares. Each server is one
// closed row that says what's inside, opened in place. Changing a server's
// scope moves it to the other part, and the cards follow it there.
import * as React from "react";
import { Link } from "@tanstack/react-router";
import { BotIcon, GlobeIcon, PlusIcon } from "lucide-react";

import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader } from "@/components/ui/empty";
import {
  Field,
  FieldContent,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { useT } from "@/lib/i18n";
import {
  ConnectorGlyph,
  GroupActions,
  GroupMeta,
  ToolRow,
  useSearchOpen,
  type ToolItem,
  type ToolsViewProps,
} from "./shared";

export function ToolsByScope(props: ToolsViewProps) {
  const t = useT();
  const { groups, query, agent } = props;
  const searching = query.trim() !== "";
  const own = groups.filter(
    (list) => props.ownerOf(list[0]!.connectorId) === agent.id,
  );
  const shared = groups.filter(
    (list) => props.ownerOf(list[0]!.connectorId) !== agent.id,
  );
  const [picked, setPicked] = React.useState<Part>(
    own.length > 0 ? "own" : "shared",
  );
  // A search with no hit in the picked part shows the other one.
  const part: Part =
    searching && (picked === "own" ? own : shared).length === 0
      ? picked === "own"
        ? "shared"
        : "own"
      : picked;
  const ids = groups.map((list) => list[0]!.connectorId);
  const [open, setOpen] = useSearchOpen(
    query,
    ids,
    ids.length === 1 ? ids : [],
  );
  // Each part reports only its own rows; keep what's open in the other one.
  const setOpenPart = (part: ToolItem[][]) => (next: unknown[]) => {
    const mine = new Set(part.map((list) => list[0]!.connectorId));
    setOpen([...open.filter((id) => !mine.has(id)), ...(next as string[])]);
  };
  // A server whose scope changed moves to the other part and remounts there.
  // Put focus back on its select so the keyboard doesn't land on the page top.
  const [moved, setMoved] = React.useState<string | null>(null);
  const scoped: ToolsViewProps = {
    ...props,
    setScope: (id, scope) => {
      setMoved(id);
      setPicked(scope === "only" ? "own" : "shared");
      props.setScope(id, scope);
    },
  };
  React.useEffect(() => {
    if (!moved) return;
    const frame = requestAnimationFrame(() => {
      const item = document.querySelector<HTMLElement>(
        `[data-connector="${CSS.escape(moved)}"]`,
      );
      const target =
        item?.querySelector<HTMLElement>("[data-slot=select-trigger]") ??
        item?.querySelector<HTMLElement>("[data-slot=accordion-trigger]");
      target?.focus({ preventScroll: true });
      item?.scrollIntoView({ block: "nearest" });
      setMoved(null);
    });
    return () => cancelAnimationFrame(frame);
  }, [moved, own.length]);
  const addMcp = (
    <Button
      variant="outline"
      size="sm"
      render={<Link to="/connectors/new" search={{ agent: agent.id }} />}
    >
      <PlusIcon data-icon="inline-start" />
      {t("agents.tools.addMcp")}
    </Button>
  );

  const parts = [
    {
      value: "own",
      Icon: BotIcon,
      title: t("agents.tools.scopeHere"),
      count: own.length,
    },
    {
      value: "shared",
      Icon: GlobeIcon,
      title: t("agents.tools.scopeAll"),
      count: shared.length,
    },
  ] as const;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <RadioGroup
          value={part}
          onValueChange={(v) => setPicked(v as Part)}
          aria-label={t("agents.tools.parts")}
          className="grid max-w-2xl min-w-0 flex-1 basis-80 grid-cols-2 gap-3"
        >
          {parts.map(({ value, Icon, title, count }) => (
            <FieldLabel
              key={value}
              htmlFor={`agent-tools-pick-${value}`}
              className="min-w-0"
            >
              <Field orientation="horizontal" className="items-center gap-3">
                <Icon
                  aria-hidden="true"
                  className="size-4 shrink-0 text-muted-foreground"
                />
                <FieldContent className="min-w-0">
                  <FieldTitle className="flex-wrap">
                    {title}
                    {count > 0 && (
                      <span className="font-normal text-muted-foreground tabular-nums">
                        {count.toLocaleString()}
                      </span>
                    )}
                  </FieldTitle>
                </FieldContent>
                <RadioGroupItem
                  value={value}
                  id={`agent-tools-pick-${value}`}
                  aria-controls={`agent-tools-${value}`}
                />
              </Field>
            </FieldLabel>
          ))}
        </RadioGroup>
        <span className="ms-auto">{addMcp}</span>
      </div>

      {part === "own" ? (
        <section
          id="agent-tools-own"
          aria-label={t("agents.tools.scopeHere")}
          className="flex flex-col gap-3"
        >
          {own.length > 0 ? (
            <Groups
              groups={own}
              open={open}
              onOpenChange={setOpenPart(own)}
              props={scoped}
            />
          ) : (
            <Empty className="border border-dashed p-6">
              <EmptyHeader>
                <EmptyDescription>{t("agents.tools.ownNone")}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </section>
      ) : (
        <section
          id="agent-tools-shared"
          aria-label={t("agents.tools.scopeAll")}
          className="flex flex-col gap-3"
        >
          {shared.length > 0 ? (
            <Groups
              groups={shared}
              open={open}
              onOpenChange={setOpenPart(shared)}
              props={scoped}
            />
          ) : (
            <Empty className="border border-dashed p-6">
              <EmptyHeader>
                <EmptyDescription>
                  {t("agents.tools.sharedNone")}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </section>
      )}
    </div>
  );
}

type Part = "own" | "shared";

function Groups({
  groups,
  open,
  onOpenChange,
  props,
}: {
  groups: ToolItem[][];
  open: string[];
  onOpenChange: (next: unknown[]) => void;
  props: ToolsViewProps;
}) {
  const mine = groups.map((list) => list[0]!.connectorId);
  return (
    <Card className="gap-0 py-0">
      <Accordion
        multiple
        hiddenUntilFound
        value={open.filter((id) => mine.includes(id))}
        onValueChange={onOpenChange}
      >
        {groups.map((list) => {
          const head = list[0]!;
          return (
            <AccordionItem
              key={head.connectorId}
              value={head.connectorId}
              data-connector={head.connectorId}
            >
              <AccordionTrigger className="items-center gap-3 rounded-none px-4 py-3.5 transition-[background-color,box-shadow] duration-150 hover:bg-muted/50 hover:no-underline focus-visible:ring-inset active:bg-muted">
                <ConnectorGlyph type={head.connectorType} />
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                    <span
                      dir="auto"
                      className="min-w-0 underline-offset-4 [overflow-wrap:anywhere] group-hover/accordion-trigger:underline"
                    >
                      {head.connectorLabel}
                    </span>
                    {head.connectorType === "mcp" && (
                      <Badge variant="outline">MCP</Badge>
                    )}
                  </span>
                  <GroupMeta list={list} />
                </span>
              </AccordionTrigger>
              <AccordionContent className="flex flex-col gap-1 px-4 pb-4 @xl/main:ps-16">
                <GroupActions head={head} props={props} />
                <ul className="flex flex-col divide-y">
                  {list.map((x) => (
                    <ToolRow key={x.name} x={x} props={props} />
                  ))}
                </ul>
              </AccordionContent>
            </AccordionItem>
          );
        })}
      </Accordion>
    </Card>
  );
}
