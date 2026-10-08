describe("test environment", () => {
  it("provides jsdom and jest-dom matchers", () => {
    document.body.innerHTML = '<main aria-label="application"></main>';

    expect(document.querySelector("main")).toBeInTheDocument();
  });
});
