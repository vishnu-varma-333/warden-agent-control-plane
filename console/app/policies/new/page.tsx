import { PageHeader } from "@/components/page-header";
import { NewPolicyForm } from "./new-policy-form";

export default function NewPolicyPage() {
  return (
    <div>
      <PageHeader title="New policy version" description="Write a Cedar policy. It won't take effect until activated." />
      <div className="max-w-3xl">
        <NewPolicyForm />
      </div>
    </div>
  );
}
