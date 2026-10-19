// The buttons on the Bond Bridge page. A button asks for something to be done; the bridge does it a moment later, so the page
// is loaded again once it has had time to.
class BondDevices {
    constructor(doc) {
        this.doc = doc;
    }

    init = () => {
        const cards = this.doc.querySelectorAll("div.entity-bond-device");

        cards.forEach((card) => {
            card.querySelectorAll("button.cmd").forEach((button) => {
                button.addEventListener("click", () => this.send(card, button));
            });
        });

        return cards.length > 0;
    }

    send = async (card, button) => {
        const error = card.querySelector(".error");
        const buttons = card.querySelectorAll("button.cmd");
        const part = button.closest(".part");
        error.textContent = "";

        const body = {
            Device: card.dataset.device,
            Target: part ? part.dataset.target : "",
            Op: button.dataset.op,
            Value: parseInt(button.dataset.value, 10),
        };

        buttons.forEach((b) => b.disabled = true);

        try {
            const response = await fetch("/plugins/bondbridge/command", {
                method: "POST",
                headers: {"Content-Type": "application/json"},
                body: JSON.stringify(body),
            });

            if (!response.ok) {
                const reason = (await response.text()).replace(/^Failed to handle request: /, "").split("\n")[0].slice(0, 200);
                error.textContent = "Could not send the command (" + response.status + "): " + reason;
                buttons.forEach((b) => b.disabled = false);
                return;
            }

            // A dimming takes a while. The page shows it is dimming, and the buttons come back when it is loaded again.
            setTimeout(() => location.reload(), body.Op === "brightness" || body.Op === "step" ? 2500 : 1200);
        } catch (e) {
            error.textContent = "Could not send the command: " + e;
            buttons.forEach((b) => b.disabled = false);
        }
    }
}

new BondDevices(document).init();
